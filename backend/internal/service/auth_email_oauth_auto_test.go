//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func newEmailOAuthAutoAuthService(
	userRepo UserRepository,
	settings map[string]string,
	quotaRepo UserPlatformQuotaRepository,
) *AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
		Default: config.DefaultConfig{
			UserBalance:     3.5,
			UserConcurrency: 2,
		},
	}

	settingService := NewSettingService(&settingRepoStub{values: settings}, cfg)

	return NewAuthService(
		nil, // entClient — nil, updateUserSignupSource early return
		userRepo,
		nil, // redeemRepo — invitationCode="" 时不触发
		&refreshTokenCacheStub{},
		cfg,
		settingService,
		nil, // emailService
		nil, // turnstileService
		nil, // emailQueueService
		nil, // promoService
		nil, // defaultSubAssigner — nil, assignSubscriptions early return
		nil, // affiliateService — nil, bindOAuthAffiliate early return
		quotaRepo,
	)
}

func TestEmailOAuthAuto_SnapshotsPlatformQuotaDefaults(t *testing.T) {
	userRepo := &userRepoStub{nextID: 88}
	quotaRepo := &userPlatformQuotaRepoStub{}

	svc := newEmailOAuthAutoAuthService(
		userRepo,
		map[string]string{
			SettingKeyRegistrationEnabled:   "true",
			SettingKeyDefaultPlatformQuotas: `{"gemini": {"monthly": 100.0}}`,
		},
		quotaRepo,
	)

	user, err := svc.createEmailOAuthUser(
		context.Background(),
		"newoauth@example.com",
		"newoauth",
		"github",
		"", // invitationCode
		"", // affiliateCode
	)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(88), user.ID)

	require.Len(t, quotaRepo.bulkInsertCalls, 1, "createEmailOAuthUser must snapshot platform quotas via BulkInsertInitial")

	records := quotaRepo.bulkInsertCalls[0]
	var geminiRecord *UserPlatformQuotaRecord
	for i := range records {
		if records[i].Platform == "gemini" {
			geminiRecord = &records[i]
			break
		}
	}
	require.NotNil(t, geminiRecord, "expected gemini platform record")
	require.NotNil(t, geminiRecord.MonthlyLimitUSD)
	require.InDelta(t, 100.0, *geminiRecord.MonthlyLimitUSD, 0.0001)
}

func TestEmailOAuthAuto_ExistingAccountOutsideRegistrationWhitelist(t *testing.T) {
	for _, provider := range []string{"google", "github"} {
		for _, bound := range []bool{true, false} {
			name := provider + "/email account"
			if bound {
				name = provider + "/bound identity"
			}
			t.Run(name, func(t *testing.T) {
				_, client := newAuthPendingIdentityServiceTestClient(t)
				ctx := context.Background()
				storedUser, err := client.User.Create().
					SetEmail("existing@example.com").
					SetPasswordHash("hash").
					SetRole(RoleUser).
					SetStatus(StatusActive).
					Save(ctx)
				require.NoError(t, err)
				if bound {
					_, err = client.AuthIdentity.Create().
						SetUserID(storedUser.ID).
						SetProviderType(provider).
						SetProviderKey(provider).
						SetProviderSubject("oauth-123").
						Save(ctx)
					require.NoError(t, err)
				}
				userRepo := &userRepoStub{user: &User{
					ID: storedUser.ID, Email: storedUser.Email, PasswordHash: storedUser.PasswordHash,
					Role: RoleUser, Status: StatusActive,
				}}
				svc := newEmailOAuthAutoAuthService(userRepo, map[string]string{
					SettingKeyRegistrationEnabled:              "true",
					SettingKeyRegistrationEmailSuffixWhitelist: `["@allowed.example"]`,
				}, nil)
				svc.entClient = client

				pair, user, err := svc.LoginOrRegisterVerifiedEmailOAuth(ctx, EmailOAuthIdentityInput{
					ProviderType: provider, ProviderKey: provider, ProviderSubject: "oauth-123",
					Email: storedUser.Email, EmailVerified: true,
				})

				require.NoError(t, err)
				require.Equal(t, storedUser.ID, user.ID)
				claims, err := svc.ValidateToken(pair.AccessToken)
				require.NoError(t, err)
				require.Equal(t, storedUser.ID, claims.UserID)
				identity, err := client.AuthIdentity.Query().Where(
					authidentity.ProviderTypeEQ(provider), authidentity.ProviderSubjectEQ("oauth-123"),
				).Only(ctx)
				require.NoError(t, err)
				require.Equal(t, storedUser.ID, identity.UserID)
				require.Empty(t, userRepo.created)
			})
		}
	}
}

func TestEmailOAuthAuto_RegistrationWhitelistAndAuthenticationGuards(t *testing.T) {
	for _, tt := range []struct {
		name          string
		existingUser  bool
		status        string
		email         string
		emailVerified bool
		wantReason    string
	}{
		{name: "new account outside whitelist", email: "new@example.com", emailVerified: true, wantReason: "EMAIL_SUFFIX_NOT_ALLOWED"},
		{name: "disabled account", existingUser: true, status: StatusDisabled, email: "existing@example.com", emailVerified: true, wantReason: "USER_NOT_ACTIVE"},
		{name: "unverified email", existingUser: true, status: StatusActive, email: "existing@example.com", wantReason: "OAUTH_EMAIL_NOT_VERIFIED"},
		{name: "identity email mismatch", existingUser: true, status: StatusActive, email: "other@example.com", emailVerified: true, wantReason: "AUTH_IDENTITY_EMAIL_MISMATCH"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, client := newAuthPendingIdentityServiceTestClient(t)
			ctx := context.Background()
			userRepo := &userRepoStub{}
			if tt.existingUser {
				storedUser, err := client.User.Create().
					SetEmail("existing@example.com").
					SetPasswordHash("hash").
					SetRole(RoleUser).
					SetStatus(tt.status).
					Save(ctx)
				require.NoError(t, err)
				_, err = client.AuthIdentity.Create().
					SetUserID(storedUser.ID).
					SetProviderType("google").
					SetProviderKey("google").
					SetProviderSubject("oauth-123").
					Save(ctx)
				require.NoError(t, err)
				userRepo.user = &User{
					ID: storedUser.ID, Email: storedUser.Email, PasswordHash: storedUser.PasswordHash,
					Role: RoleUser, Status: tt.status,
				}
			}
			svc := newEmailOAuthAutoAuthService(userRepo, map[string]string{
				SettingKeyRegistrationEnabled:              "true",
				SettingKeyRegistrationEmailSuffixWhitelist: `["@allowed.example"]`,
			}, nil)
			svc.entClient = client

			pair, user, err := svc.LoginOrRegisterVerifiedEmailOAuth(ctx, EmailOAuthIdentityInput{
				ProviderType: "google", ProviderKey: "google", ProviderSubject: "oauth-123",
				Email: tt.email, EmailVerified: tt.emailVerified,
			})

			require.Error(t, err)
			require.Equal(t, tt.wantReason, infraerrors.Reason(err))
			require.Nil(t, pair)
			require.Nil(t, user)
			require.Empty(t, userRepo.created)
		})
	}
}

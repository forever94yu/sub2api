package setup

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin/binding"
)

func TestPrepareAdminCredentialsGeneratesOnlyMissingValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		admin             AdminConfig
		wantEmail         string
		emailGenerated    bool
		passwordGenerated bool
	}{
		{name: "both missing", emailGenerated: true, passwordGenerated: true},
		{name: "whitespace only", admin: AdminConfig{Email: " \t", Password: " \n"}, emailGenerated: true, passwordGenerated: true},
		{name: "email missing", admin: AdminConfig{Password: "12345678"}, emailGenerated: true},
		{name: "password missing", admin: AdminConfig{Email: "owner@example.com"}, wantEmail: "owner@example.com", passwordGenerated: true},
		{name: "both provided", admin: AdminConfig{Email: "owner@example.com", Password: "a-strong-password"}, wantEmail: "owner@example.com"},
		{name: "trim email only", admin: AdminConfig{Email: " owner@example.com\n", Password: " password with spaces "}, wantEmail: "owner@example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			admin := tc.admin
			emailGenerated, passwordGenerated, err := prepareAdminCredentials(&admin)
			if err != nil {
				t.Fatalf("prepareAdminCredentials() error = %v", err)
			}
			if emailGenerated != tc.emailGenerated || passwordGenerated != tc.passwordGenerated {
				t.Fatalf("generated flags = (%v, %v), want (%v, %v)", emailGenerated, passwordGenerated, tc.emailGenerated, tc.passwordGenerated)
			}
			if tc.emailGenerated {
				if !regexp.MustCompile(`^admin-[0-9a-f]{12}@sub2api\.local$`).MatchString(admin.Email) {
					t.Errorf("generated email = %q, want admin-<12 hex>@sub2api.local", admin.Email)
				}
			} else if admin.Email != tc.wantEmail {
				t.Errorf("email = %q, want %q", admin.Email, tc.wantEmail)
			}
			if tc.passwordGenerated {
				if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(admin.Password) {
					t.Error("generated password must contain 32 hexadecimal characters")
				}
			} else if admin.Password != tc.admin.Password {
				t.Error("provided password was modified")
			}
			login := struct {
				Email string `binding:"required,email"`
			}{Email: admin.Email}
			if err := binding.Validator.ValidateStruct(&login); err != nil {
				t.Errorf("prepared email rejected by login validator: %v", err)
			}
		})
	}
}

func TestPrepareAdminCredentialsGeneratesDistinctValues(t *testing.T) {
	t.Parallel()

	first, second := AdminConfig{}, AdminConfig{}
	for _, admin := range []*AdminConfig{&first, &second} {
		if _, _, err := prepareAdminCredentials(admin); err != nil {
			t.Fatalf("prepareAdminCredentials() error = %v", err)
		}
	}
	if first.Email == second.Email {
		t.Error("fresh installations received the same admin email")
	}
	if first.Password == second.Password {
		t.Error("fresh installations received the same admin password")
	}
}

func newAdminBootstrapTestDB(t *testing.T, totalUsers, adminUsers int64) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("database expectations not met: %v", err)
		}
		_ = db.Close()
	})
	mock.ExpectQuery("SELECT COUNT(1) FROM users").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(totalUsers))
	mock.ExpectQuery("SELECT COUNT(1) FROM users WHERE role = $1").
		WithArgs(service.RoleAdmin).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(adminUsers))
	return db, mock
}

func TestBootstrapAdminUserSkipsCredentialPreparationWhenNotCreating(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		totalUsers int64
		adminUsers int64
		reason     string
	}{
		{name: "admin exists", totalUsers: 3, adminUsers: 1, reason: adminBootstrapReasonAdminExists},
		{name: "users exist without admin", totalUsers: 3, reason: adminBootstrapReasonUsersExistWithoutAdmin},
	}
	credentials := []struct {
		name  string
		admin AdminConfig
	}{
		{name: "invalid legacy values", admin: AdminConfig{Email: "admin", Password: "123456"}},
		{name: "missing values"},
	}

	for _, tc := range tests {
		for _, creds := range credentials {
			t.Run(tc.name+"/"+creds.name, func(t *testing.T) {
				db, _ := newAdminBootstrapTestDB(t, tc.totalUsers, tc.adminUsers)
				cfg := &SetupConfig{Admin: creds.admin}
				created, reason, err := bootstrapAdminUser(context.Background(), db, cfg)
				if err != nil || created || reason != tc.reason {
					t.Fatalf("bootstrapAdminUser() = (%v, %q, %v), want (false, %q, nil)", created, reason, err, tc.reason)
				}
				if cfg.Admin != creds.admin {
					t.Error("skipped bootstrap modified admin credentials")
				}
			})
		}
	}
}

func TestBootstrapAdminUserRejectsInvalidCredentialsBeforeInsert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		admin   AdminConfig
		wantErr string
	}{
		{name: "missing email domain", admin: AdminConfig{Email: "admin", Password: "valid-password"}, wantErr: "invalid admin email"},
		{name: "single label domain", admin: AdminConfig{Email: "a@b", Password: "valid-password"}, wantErr: "invalid admin email"},
		{name: "display name", admin: AdminConfig{Email: "Owner <owner@example.com>", Password: "valid-password"}, wantErr: "invalid admin email"},
		{name: "short password", admin: AdminConfig{Email: "owner@example.com", Password: "123456"}, wantErr: "invalid admin password"},
		{name: "short password and generated email", admin: AdminConfig{Password: "123456"}, wantErr: "invalid admin password"},
		{name: "73 ASCII bytes", admin: AdminConfig{Email: "owner@example.com", Password: strings.Repeat("a", 73)}, wantErr: "invalid admin password"},
		{name: "73 Unicode bytes", admin: AdminConfig{Email: "owner@example.com", Password: strings.Repeat("\u4e2d", 24) + "a"}, wantErr: "invalid admin password"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := newAdminBootstrapTestDB(t, 0, 0)
			cfg := &SetupConfig{Admin: tc.admin}
			created, _, err := bootstrapAdminUser(context.Background(), db, cfg)
			if err == nil || created || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("bootstrapAdminUser() = (%v, %v), want %s", created, err, tc.wantErr)
			}
		})
	}
}

type capturedAdminString struct {
	value string
}

func (arg *capturedAdminString) Match(value driver.Value) bool {
	text, ok := value.(string)
	if ok {
		arg.value = text
	}
	return ok
}

func TestBootstrapAdminUserPersistsUsableCredentials(t *testing.T) {
	t.Setenv("RUN_MODE", "standard")

	tests := []struct {
		name  string
		admin AdminConfig
	}{
		{name: "generated credentials"},
		{name: "whitespace generates credentials", admin: AdminConfig{Email: " \t", Password: " \n"}},
		{name: "provided credentials", admin: AdminConfig{Email: "owner@example.com", Password: "a-strong-password"}},
		{name: "minimum password", admin: AdminConfig{Password: "12345678"}},
		{name: "72 ASCII bytes", admin: AdminConfig{Email: "owner@example.com", Password: strings.Repeat("a", 72)}},
		{name: "72 Unicode bytes", admin: AdminConfig{Email: "owner@example.com", Password: strings.Repeat("\u4e2d", 24)}},
		{name: "preserve password spaces", admin: AdminConfig{Email: " owner@example.com ", Password: " password with spaces "}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newAdminBootstrapTestDB(t, 0, 0)
			email, hash := &capturedAdminString{}, &capturedAdminString{}
			mock.ExpectExec(`INSERT INTO users (email, password_hash, role, balance, concurrency, status, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`).
				WithArgs(email, hash, service.RoleAdmin, float64(0), 5, service.StatusActive, sqlmock.AnyArg(), sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(1, 1))

			cfg := &SetupConfig{Admin: tc.admin}
			created, reason, err := bootstrapAdminUser(context.Background(), db, cfg)
			if err != nil || !created || reason != adminBootstrapReasonEmptyDatabase {
				t.Fatalf("bootstrapAdminUser() = (%v, %q, %v), want (true, %q, nil)", created, reason, err, adminBootstrapReasonEmptyDatabase)
			}
			if email.value != cfg.Admin.Email {
				t.Errorf("persisted email = %q, want prepared email %q", email.value, cfg.Admin.Email)
			}
			if strings.TrimSpace(tc.admin.Email) == "" {
				if !regexp.MustCompile(`^admin-[0-9a-f]{12}@sub2api\.local$`).MatchString(email.value) {
					t.Errorf("persisted email = %q, want generated admin email", email.value)
				}
			} else if email.value != strings.TrimSpace(tc.admin.Email) {
				t.Errorf("provided email was not preserved: %q", email.value)
			}
			if strings.TrimSpace(tc.admin.Password) != "" && cfg.Admin.Password != tc.admin.Password {
				t.Error("provided password was not preserved")
			}
			if hash.value == cfg.Admin.Password {
				t.Error("persisted password was not hashed")
			}
			user := service.User{PasswordHash: hash.value}
			if !user.CheckPassword(cfg.Admin.Password) {
				t.Error("persisted password hash does not authenticate the prepared password")
			}
		})
	}
}

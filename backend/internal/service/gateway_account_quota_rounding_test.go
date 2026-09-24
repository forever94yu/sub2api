package service

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildUsageBillingCommandAccountQuotaMatchesStoredPrecision(t *testing.T) {
	for _, tt := range []struct {
		name       string
		totalCost  float64
		multiplier float64
		want       float64
	}{
		{"reported Claude half boundary", 0.00004675, 0.7, 0.00003273},
		{"binary total below boundary", math.Nextafter(0.00004675, 0), 0.7, 0.00003273},
		{"binary total above boundary", math.Nextafter(0.00004675, 1), 0.7, 0.00003273},
		{"stored total rounded to half boundary", 0.00004674996, 0.7, 0.00003273},
		{"below stored half boundary", 0.0000467499, 0.7, 0.00003272},
		{"above stored half boundary", 0.0000467501, 0.7, 0.00003273},
		{"stored multiplier precision", 1, 0.700049, 0.7},
		{"free account", 0.00004675, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := accountQuotaRoundingParams(tt.totalCost, tt.multiplier)
			raw := &UsageBillingCommand{
				RequestID: "account-rounding", UserID: p.User.ID, APIKeyID: p.APIKey.ID,
				AccountID: p.Account.ID, AccountType: p.Account.Type,
				AccountQuotaCost: p.Cost.TotalCost * p.AccountRateMultiplier,
			}
			wantFingerprint := buildUsageBillingFingerprint(raw)

			cmd := buildUsageBillingCommand("account-rounding", nil, p)
			require.NotNil(t, cmd)
			require.Equal(t, wantFingerprint, cmd.RequestFingerprint, "retries must retain the raw amount fingerprint")
			require.Equal(t, tt.want, cmd.AccountQuotaCost)
			cmd.Normalize()
			require.Equal(t, wantFingerprint, cmd.RequestFingerprint)
			require.Equal(t, tt.want, cmd.AccountQuotaCost)
		})
	}
}

func TestPostUsageBillingAccountQuotaMatchesStoredPrecision(t *testing.T) {
	repo := &accountQuotaRoundingRepo{}
	p := accountQuotaRoundingParams(0.00004675, 0.7)
	postUsageBilling(context.Background(), p, &billingDeps{accountRepo: repo})
	require.Equal(t, p.Account.ID, repo.accountID)
	require.Equal(t, 0.00003273, repo.amount)
}

func TestCalculateAccountQuotaCostPreservesNonFiniteValues(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		totalCost, multiplier float64
	}{
		{"NaN total", math.NaN(), 0.7},
		{"NaN multiplier", 1, math.NaN()},
		{"infinite total", math.Inf(1), 0.7},
		{"infinite multiplier", 1, math.Inf(-1)},
		{"zero times infinity", 0, math.Inf(1)},
		{"infinity times zero", math.Inf(1), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.totalCost * tt.multiplier
			got := calculateAccountQuotaCost(tt.totalCost, tt.multiplier)
			if math.IsNaN(want) {
				require.True(t, math.IsNaN(got))
			} else {
				require.Equal(t, want, got)
			}
		})
	}
}

func accountQuotaRoundingParams(totalCost, multiplier float64) *postUsageBillingParams {
	return &postUsageBillingParams{
		Cost: &CostBreakdown{TotalCost: totalCost}, User: &User{ID: 1}, APIKey: &APIKey{ID: 2},
		Account:               &Account{ID: 3, Type: AccountTypeAPIKey, Extra: map[string]any{"quota_daily_limit": float64(100)}},
		AccountRateMultiplier: multiplier,
	}
}

type accountQuotaRoundingRepo struct {
	AccountRepository
	accountID int64
	amount    float64
}

func (r *accountQuotaRoundingRepo) IncrementQuotaUsed(_ context.Context, accountID int64, amount float64) error {
	r.accountID = accountID
	r.amount = amount
	return nil
}

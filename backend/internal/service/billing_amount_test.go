package service

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestTokenSettlementRetainsSubFloatBoundary(t *testing.T) {
	billing := &BillingService{}
	cost := billing.computeTokenBreakdown(&ModelPricing{InputPricePerToken: 1e-7}, UsageTokens{InputTokens: 1}, 0.3, "", false)
	applyCostBreakdownMultiplier(cost, 0.16666666666666666)
	// Exact amount is 0.0000000049999999999999998. Its float projection lands
	// on the half boundary, but the debit must still round down.
	require.Equal(t, 0.000000005, cost.actualBillingAmount().precise.InexactFloat64())
	require.Equal(t, 0.0, cost.settledActualCost())
}

func TestTokenSettlementHonorsPublicAmountOverrides(t *testing.T) {
	billing := &BillingService{}
	cost := billing.computeTokenBreakdown(&ModelPricing{InputPricePerToken: 1e-7}, UsageTokens{InputTokens: 1}, 1, "", false)
	cost.TotalCost = 0.7
	cost.ActualCost = 0.0000000449955
	applyCostBreakdownMultiplier(cost, 2)
	require.Equal(t, 1.4, cost.TotalCost)
	require.Equal(t, 0.00000009, cost.settledActualCost())
}

func TestTokenSettlementIncludesAddedSearchCost(t *testing.T) {
	billing := &BillingService{}
	cost := billing.computeTokenBreakdown(&ModelPricing{CacheCreationPricePerToken: 0.625e-6, OutputPricePerToken: 2.5e-6},
		UsageTokens{CacheCreationTokens: 100001, OutputTokens: 100}, 1, "", false)
	addCostBreakdownTotals(cost, &CostBreakdown{TotalCost: 0.005, ActualCost: 0.005})
	applyCostBreakdownMultiplier(cost, 1.1)
	require.True(t, decimal.RequireFromString("0.0745256875").Equal(*cost.totalBillingAmount().precise))
	require.Equal(t, 0.07452569, cost.settledActualCost())
}

func TestTokenSettlementPreservesNonFiniteInputs(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run("price", func(t *testing.T) {
			var cost *CostBreakdown
			require.NotPanics(t, func() {
				cost = (&BillingService{}).computeTokenBreakdown(&ModelPricing{InputPricePerToken: value}, UsageTokens{InputTokens: 1}, 1, "", false)
				applyCostBreakdownMultiplier(cost, 1.1)
			})
			if math.IsNaN(value) {
				require.True(t, math.IsNaN(cost.settledActualCost()))
			} else {
				require.Equal(t, value, cost.settledActualCost())
			}
		})
	}
	cost := (&BillingService{}).computeTokenBreakdown(&ModelPricing{InputPricePerToken: 1}, UsageTokens{InputTokens: 1}, math.Inf(1), "", false)
	require.True(t, math.IsInf(cost.settledActualCost(), 1))
}

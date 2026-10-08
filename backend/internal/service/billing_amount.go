package service

import (
	"math"

	"github.com/shopspring/decimal"
)

// Keep the original float operations for retry fingerprints, and carry exact
// decimal amounts separately until settlement. Non-finite inputs stay floats.
type billingAmount struct {
	value   float64
	precise *decimal.Decimal
}

func newBillingAmount(value float64) billingAmount {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return billingAmount{value: value}
	}
	return exactBillingAmount(value, decimal.NewFromFloat(value))
}

func exactBillingAmount(value float64, precise decimal.Decimal) billingAmount {
	return billingAmount{value: value, precise: &precise}
}

func (a billingAmount) add(b billingAmount) billingAmount {
	if a.precise != nil && b.precise != nil {
		return exactBillingAmount(a.value+b.value, a.precise.Add(*b.precise))
	}
	return newBillingAmount(a.value + b.value)
}

func (a billingAmount) mul(b billingAmount) billingAmount {
	if a.precise != nil && b.precise != nil {
		return exactBillingAmount(a.value*b.value, a.precise.Mul(*b.precise))
	}
	return newBillingAmount(a.value * b.value)
}

func (a billingAmount) tokens(count int) billingAmount {
	if a.precise != nil {
		return exactBillingAmount(a.value*float64(count), a.precise.Mul(decimal.NewFromInt(int64(count))))
	}
	return newBillingAmount(a.value * float64(count))
}

func (c *CostBreakdown) totalBillingAmount() billingAmount {
	if c.preciseTotal.precise != nil && c.preciseTotal.value == c.TotalCost {
		return c.preciseTotal
	}
	return newBillingAmount(c.TotalCost)
}

func (c *CostBreakdown) actualBillingAmount() billingAmount {
	// Callers may still overwrite the public float fields. Never reuse an exact
	// amount after its public float has been changed.
	if c.preciseActual.precise != nil && c.preciseActual.value == c.ActualCost {
		return c.preciseActual
	}
	return newBillingAmount(c.ActualCost)
}

func (c *CostBreakdown) setBillingAmounts(total, actual billingAmount) {
	c.TotalCost, c.ActualCost = total.value, actual.value
	c.preciseTotal, c.preciseActual = total, actual
}

func (c *CostBreakdown) settledActualCost() float64 {
	amount := c.actualBillingAmount()
	if amount.precise != nil {
		return amount.precise.Round(UsageBillingMonetaryScale).InexactFloat64()
	}
	return QuantizeUsageBillingAmount(amount.value)
}

func addCostBreakdownTotals(cost, additional *CostBreakdown) {
	cost.setBillingAmounts(cost.totalBillingAmount().add(additional.totalBillingAmount()),
		cost.actualBillingAmount().add(additional.actualBillingAmount()))
}

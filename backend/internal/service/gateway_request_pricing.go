package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

type gatewayTokenRequestPricingAtCtxKey struct{}
type gatewayTokenRequestBillingGroupCtxKey struct{}

// WithGatewayTokenRequestPricing marks a shared-gateway request as token billed
// and freezes the downstream pricing instant for its whole lifetime. Media and
// metadata-only handlers deliberately do not call this helper.
func WithGatewayTokenRequestPricing(ctx context.Context) (context.Context, time.Time) {
	if ctx == nil {
		ctx = context.Background()
	}
	pricingAt := timezone.Now()
	ctx = context.WithValue(ctx, gatewayTokenRequestPricingAtCtxKey{}, pricingAt)
	// 调度过程中可能因 fallback/composite 路由覆盖 ctxkey.Group；计费 D 仍必须
	// 使用认证时刻的父分组，和最终 RecordUsage 的计费归属保持一致。
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) {
		ctx = context.WithValue(ctx, gatewayTokenRequestBillingGroupCtxKey{}, group)
	}
	return ctx, pricingAt
}

func gatewayTokenRequestPricingAtFromContext(ctx context.Context) (time.Time, bool) {
	if ctx == nil {
		return time.Time{}, false
	}
	pricingAt, ok := ctx.Value(gatewayTokenRequestPricingAtCtxKey{}).(time.Time)
	return pricingAt, ok && !pricingAt.IsZero()
}

// GatewayTokenRequestPricingAtFromContext exposes the frozen instant to
// handlers before they detach asynchronous usage-recording work.
func GatewayTokenRequestPricingAtFromContext(ctx context.Context) time.Time {
	pricingAt, _ := gatewayTokenRequestPricingAtFromContext(ctx)
	return pricingAt
}

func gatewayTokenRequestBillingGroupFromContext(ctx context.Context) *Group {
	if ctx == nil {
		return nil
	}
	group, _ := ctx.Value(gatewayTokenRequestBillingGroupCtxKey{}).(*Group)
	if IsGroupContextValid(group) {
		return group
	}
	return nil
}

// Reject unpriced billable Claude requests before the provider performs work.
// Metadata/count-token requests do not carry the token-pricing context marker.
func (s *GatewayService) ensureClaudeRequestPricing(ctx context.Context, account *Account, requestedModel, upstreamModel string) error {
	if account == nil || account.Platform != PlatformAnthropic {
		return nil
	}
	if _, billable := gatewayTokenRequestPricingAtFromContext(ctx); !billable {
		return nil
	}
	group := gatewayTokenRequestBillingGroupFromContext(ctx)
	for _, model := range []string{upstreamModel, requestedModel} {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if s.resolver != nil {
			input := PricingInput{Model: model, Group: group}
			if group != nil {
				input.GroupID = &group.ID
			}
			resolved := s.resolver.Resolve(ctx, input)
			if resolved.Source == PricingSourceGroup || resolved.Source == PricingSourceChannel {
				return nil
			}
		}
		if s.billingService != nil {
			if _, err := s.billingService.GetModelPricing(model); err == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("%w for Claude request model: %s", ErrModelPricingUnavailable, requestedModel)
}

//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestWithGatewayTokenRequestPricingMarksOnlyExplicitTokenRequests(t *testing.T) {
	ctx, pricingAt := WithGatewayTokenRequestPricing(context.Background())

	got, ok := gatewayTokenRequestPricingAtFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, pricingAt, got)
	require.Equal(t, pricingAt, GatewayTokenRequestPricingAtFromContext(ctx))
	require.True(t, GatewayTokenRequestPricingAtFromContext(context.Background()).IsZero())
}

func TestEnsureClaudeRequestPricingRejectsUnpricedBillableRequests(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	svc := &GatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	account := &Account{Platform: PlatformAnthropic}
	ctx, _ := WithGatewayTokenRequestPricing(context.Background())

	require.ErrorIs(t, svc.ensureClaudeRequestPricing(ctx, account, "claude-unknown", "claude-unknown"), ErrModelPricingUnavailable)
	require.NoError(t, svc.ensureClaudeRequestPricing(ctx, account, "my-claude-alias", "claude-opus-5-5"))
	require.NoError(t, svc.ensureClaudeRequestPricing(context.Background(), account, "claude-unknown", "claude-unknown"))
	require.NoError(t, svc.ensureClaudeRequestPricing(ctx, &Account{Platform: PlatformOpenAI}, "unpriced", "unpriced"))
}

func TestEnsureClaudeRequestPricingAcceptsExplicitGroupPrice(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	svc := &GatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	price := 8e-6
	group := &Group{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true, ModelPricing: []ChannelModelPricing{{Models: []string{"claude-private"}, InputPrice: &price, OutputPrice: &price}}}
	ctx, _ := WithGatewayTokenRequestPricing(context.WithValue(context.Background(), ctxkey.Group, group))
	require.NoError(t, svc.ensureClaudeRequestPricing(ctx, &Account{Platform: PlatformAnthropic}, "claude-private", "claude-private"))
}

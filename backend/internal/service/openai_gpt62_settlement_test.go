//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGPT62RecordUsageBillsBothWriteLifetimes(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, nil, nil, nil)
	svc.cfg.Default.RateMultiplier = 1
	usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":12000,"output_tokens":100,"input_tokens_details":{"cached_tokens":4000,"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}}}`))
	require.True(t, ok)
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "gpt62-ttl", Model: "gpt-6.2-sol", UpstreamModel: "gpt-6.2-sol", Usage: usage},
		APIKey: &APIKey{ID: 2, Quota: 100}, User: &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI},
		APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
	})
	require.NoError(t, err)
	require.NotNil(t, logs.lastLog)
	require.InDelta(t, .0275, logs.lastLog.CacheCreationCost, 1e-12)
	require.InDelta(t, .0309, logs.lastLog.TotalCost, 1e-12)
	require.Equal(t, .0309, billingRepo.lastCmd.BalanceCost)
	require.Equal(t, 7000, logs.lastLog.CacheCreationTokens)
	require.Equal(t, 1000, logs.lastLog.InputTokens)
}

func TestImage26RecordUsageBillsNativeTokensAndSVGAuthor(t *testing.T) {
	for _, withSVG := range []bool{false, true} {
		logs := &openAIRecordUsageLogRepoStub{inserted: true}
		billingRepo := &openAIRecordUsageBillingRepoStub{}
		svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, nil, nil, nil)
		svc.cfg.Default.RateMultiplier = 1
		usage := OpenAIUsage{InputTokens: 1500, ImageInputTokens: 1000, OutputTokens: 800, ImageOutputTokens: 800}
		want := .0345
		if withSVG {
			usage.SVGUsage = &OpenAISVGUsage{Model: "gpt-6.2-sol", InputTokens: 1000, OutputTokens: 200}
			want += .004
		}
		err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: &OpenAIForwardResult{RequestID: "image26-svg", Model: "gpt-image-2.6-sunburst", UpstreamModel: "gpt-image-2.6-sunburst", ImageCount: 1, Usage: usage},
			APIKey: &APIKey{ID: 2, Quota: 100}, User: &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI},
			APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, PricingAt: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
		})
		require.NoError(t, err)
		require.InDelta(t, want, logs.lastLog.TotalCost, 1e-12)
		require.InDelta(t, want, billingRepo.lastCmd.BalanceCost, 1e-12)
		require.InDelta(t, want, logs.lastLog.InputCost+logs.lastLog.ImageInputCost+logs.lastLog.OutputCost+logs.lastLog.ImageOutputCost, 1e-12)
		require.Equal(t, "token", *logs.lastLog.BillingMode)
	}
}

func TestImage26SVGAccountStatsAndCustomAuthorPricing(t *testing.T) {
	for _, applyCustomerPrice := range []bool{false, true} {
		for _, customAuthor := range []bool{false, true} {
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, &openAIRecordUsageBillingRepoStub{}, nil, nil, nil)
			svc.cfg.Default.RateMultiplier = 1
			groupID := int64(1)
			group := &Group{ID: groupID, RateMultiplier: 1}
			author := "gpt-6.2-sol"
			if customAuthor {
				author = "custom-svg-author"
				inputPrice, outputPrice := 2e-6, 10e-6
				group.ModelPricing = []ChannelModelPricing{{Models: []string{author}, InputPrice: &inputPrice, OutputPrice: &outputPrice}}
			}
			svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive, ApplyPricingToAccountStats: applyCustomerPrice}, groupID, PlatformOpenAI)
			svc.resolver = NewModelPricingResolver(svc.channelService, svc.billingService)
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "image26-stats", Model: "gpt-image-2.6-sunburst", UpstreamModel: "gpt-image-2.6-sunburst", ImageCount: 1,
					Usage: OpenAIUsage{InputTokens: 1500, ImageInputTokens: 1000, OutputTokens: 800, ImageOutputTokens: 800,
						SVGUsage: &OpenAISVGUsage{Model: author, InputTokens: 1000, OutputTokens: 200}}},
				APIKey: &APIKey{ID: 2, Quota: 100, GroupID: &groupID, Group: group}, User: &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI},
				APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
			})
			require.NoError(t, err)
			require.InDelta(t, .0385, logs.lastLog.TotalCost, 1e-12)
			require.Equal(t, 2500, logs.lastLog.InputTokens)
			require.Equal(t, 1000, logs.lastLog.OutputTokens)
			require.NotNil(t, logs.lastLog.AccountStatsCost)
			require.InDelta(t, .0385, *logs.lastLog.AccountStatsCost, 1e-12)
		}
	}
}

func TestImage26RecordUsagePreservesExplicitGroupImagePrices(t *testing.T) {
	price, free := .02, 0.0
	for _, tc := range []struct {
		name          string
		price         *float64
		otherSize     bool
		hydrate       bool
		explicitToken bool
		want          float64
		mode          BillingMode
	}{
		{name: "official default", want: .0385, mode: BillingModeToken},
		{name: "explicit image price", price: &price, want: .04, mode: BillingModeImage},
		{name: "explicit free images", price: &free, want: 0, mode: BillingModeImage},
		{name: "different size remains token", price: &price, otherSize: true, want: .0385, mode: BillingModeToken},
		{name: "incomplete auth snapshot", price: &price, hydrate: true, want: .04, mode: BillingModeImage},
		{name: "explicit token card wins", price: &price, explicitToken: true, want: .0385, mode: BillingModeToken},
		{name: "hydrated token card wins", price: &price, hydrate: true, explicitToken: true, want: .0385, mode: BillingModeToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, nil, nil, nil)
			svc.cfg.Default.RateMultiplier = 1
			groupID := int64(1)
			group := &Group{ID: groupID, RateMultiplier: 1, ImagePrice1K: tc.price}
			if tc.otherSize {
				group.ImagePrice1K, group.ImagePrice2K = nil, tc.price
			}
			if tc.explicitToken {
				imageInput, imageOutput := 8e-6, 30e-6
				group.ModelPricing = []ChannelModelPricing{{Models: []string{"gpt-image-2.6-sunburst"}, BillingMode: BillingModeToken, ImageInputPrice: &imageInput, ImageOutputPrice: &imageOutput}}
			}
			if tc.hydrate {
				svc.channelService = &ChannelService{groupRepo: &openAIMediaPriceGroupRepoStub{group: group}}
				cache := newEmptyChannelCache()
				cache.loadedAt = time.Now()
				svc.channelService.cache.Store(cache)
				group = &Group{ID: groupID, RateMultiplier: 1}
			}
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "image26-group-price", Model: "gpt-image-2.6-sunburst", UpstreamModel: "gpt-image-2.6-sunburst", ImageCount: 2, ImageSize: "1K",
					Usage: OpenAIUsage{InputTokens: 1500, ImageInputTokens: 1000, OutputTokens: 800, ImageOutputTokens: 800,
						SVGUsage: &OpenAISVGUsage{Model: "gpt-6.2-sol", InputTokens: 1000, OutputTokens: 200}}},
				APIKey: &APIKey{ID: 2, Quota: 100, GroupID: &groupID, Group: group}, User: &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI},
				APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
			})
			require.NoError(t, err)
			require.InDelta(t, tc.want, logs.lastLog.TotalCost, 1e-12)
			require.InDelta(t, tc.want, billingRepo.lastCmd.BalanceCost, 1e-12)
			require.Equal(t, string(tc.mode), *logs.lastLog.BillingMode)
			require.Equal(t, 2500, logs.lastLog.InputTokens)
			require.Equal(t, 1000, logs.lastLog.OutputTokens)
		})
	}
}

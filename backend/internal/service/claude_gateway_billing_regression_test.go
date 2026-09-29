//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClaudeGatewayBillingRegressionServedSpeedAndGeo(t *testing.T) {
	for _, tt := range []struct {
		name, requestedSpeed, servedSpeed, requestedGeo, servedGeo string
		multiplier                                                 float64
	}{
		{"fast us", "fast", "fast", "us", "us", 2.2},
		{"fast downgraded", "fast", "standard", "global", "global", 1},
		{"us downgraded", "fast", "fast", "us", "global", 2},
		{"both downgraded", "fast", "standard", "us", "global", 1},
		{"no response metadata", "fast", "", "us", "", 2.2},
		{"no implicit upgrade", "standard", "fast", "global", "us", 1},
		{"capacity priority", "priority", "standard", "global", "global", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
			result := &ForwardResult{
				RequestID: "claude-billing-" + tt.name, Model: "claude-opus-5-5", Duration: time.Second,
				Usage:       ClaudeUsage{InputTokens: 1000, OutputTokens: 200, CacheCreationInputTokens: 500, CacheCreation5mTokens: 200, CacheCreation1hTokens: 300, CacheReadInputTokens: 1000},
				ServiceTier: &tt.requestedSpeed, UpstreamResponseServiceTier: tt.servedSpeed,
				InferenceGeo: tt.requestedGeo, UpstreamResponseInferenceGeo: tt.servedGeo,
			}
			err := svc.RecordUsage(context.Background(), &RecordUsageInput{
				Result: result, APIKey: &APIKey{ID: 1, Quota: 100}, User: &User{ID: 2}, Account: &Account{ID: 3, Platform: PlatformAnthropic},
			})
			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			standard := (1000*4.0 + 200*20.0 + 200*5.0 + 300*8.0 + 1000*0.2) / 1e6
			require.InDelta(t, standard*tt.multiplier, usageRepo.lastLog.TotalCost, 1e-12)
			require.InDelta(t, standard*tt.multiplier*1.1, usageRepo.lastLog.ActualCost, 1e-12)
			require.InDelta(t, usageRepo.lastLog.ActualCost, userRepo.lastAmount, 1e-12)
			require.Equal(t, result.ServiceTier, usageRepo.lastLog.ServiceTier)
		})
	}
}

func TestClaudeGatewayBillingRegressionResponseModelUsesItsOwnFastEligibility(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
	fast := "fast"
	result := &ForwardResult{
		RequestID: "claude-response-downgrade-fast", Model: "claude-opus-5-5", UpstreamModel: "claude-opus-5-5",
		UpstreamResponseModel: "claude-sonnet-5", ServiceTier: &fast, Duration: time.Second,
		Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 200, CacheCreationInputTokens: 500, CacheCreation5mTokens: 200, CacheCreation1hTokens: 300, CacheReadInputTokens: 1000},
	}
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: result, APIKey: &APIKey{ID: 1, Quota: 100}, User: &User{ID: 2}, Account: &Account{ID: 3, Platform: PlatformAnthropic},
		ChannelUsageFields: ChannelUsageFields{BillingModelSource: BillingModelSourceResponse},
	})
	require.NoError(t, err)
	standardSonnetCost := (1000*2.0 + 200*10.0 + 200*2.5 + 300*4.0 + 1000*0.2) / 1e6
	require.InDelta(t, standardSonnetCost*1.1, userRepo.lastAmount, 1e-12)
	require.Equal(t, "claude-opus-5-5", result.UpstreamModel, "billing must retain the original audit fields")
	require.NotNil(t, usageRepo.lastLog.ServiceTier)
	require.Equal(t, "standard", *usageRepo.lastLog.ServiceTier)
}

func TestClaudeSonnet55ResponseBillingRetainsAuditAndUsesServedModel(t *testing.T) {
	for _, tt := range []struct {
		name, requestedModel, responseModel string
		wantTotal                           float64
	}{
		{"opus downgraded to sonnet", "claude-opus-5-5", "claude-sonnet-5-5", 0.0059 * 1.1},
		{"sonnet unknown response", "claude-sonnet-5-5", "claude-sonnet-5-6", 0.0059 * 1.1},
		{"sonnet response cannot raise price", "claude-sonnet-5-5", "claude-opus-5-5", 0.0059 * 1.1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
			fast := "fast"
			result := &ForwardResult{
				RequestID: tt.name, Model: tt.requestedModel, UpstreamModel: tt.requestedModel,
				UpstreamResponseModel: tt.responseModel, ServiceTier: &fast, InferenceGeo: "us", Duration: time.Second,
				Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 200, CacheCreationInputTokens: 500, CacheCreation5mTokens: 200, CacheCreation1hTokens: 300, CacheReadInputTokens: 1000},
			}
			err := svc.RecordUsage(context.Background(), &RecordUsageInput{
				Result: result, APIKey: &APIKey{ID: 1, Quota: 100}, User: &User{ID: 2}, Account: &Account{ID: 3, Platform: PlatformAnthropic},
				ChannelUsageFields: ChannelUsageFields{BillingModelSource: BillingModelSourceResponse},
			})
			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.InDelta(t, tt.wantTotal, usageRepo.lastLog.TotalCost, 1e-12)
			require.InDelta(t, tt.wantTotal*1.1, userRepo.lastAmount, 1e-12)
			require.Equal(t, tt.requestedModel, usageRepo.lastLog.Model)
			require.Equal(t, tt.requestedModel, usageRepo.lastLog.RequestedModel)
			require.Equal(t, tt.requestedModel, result.UpstreamModel)
			require.NotNil(t, usageRepo.lastLog.UpstreamResponseModel)
			require.Equal(t, tt.responseModel, *usageRepo.lastLog.UpstreamResponseModel)
			if tt.requestedModel == "claude-opus-5-5" {
				require.NotNil(t, usageRepo.lastLog.ServiceTier)
				require.Equal(t, "standard", *usageRepo.lastLog.ServiceTier)
			}
		})
	}
}

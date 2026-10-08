package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// These undated Bedrock IDs use the native Messages API, including JSON SSE.
func isBedrockMantleModelID(modelID string) bool {
	return modelID == "anthropic.claude-haiku-5-5" || modelID == "anthropic.claude-sonnet-5-5"
}

func prepareBedrockMantleRequestBody(body []byte, modelID string, betaTokens []string) ([]byte, error) {
	body, _ = sanitizeAnthropicBodyForBetaTokens(body, strings.Join(betaTokens, ","))
	body = sanitizeBedrockDirectAPIFields(body)
	for _, field := range []string{"anthropic_version", "anthropic_beta", "provider", "fallbacks", "fallback_credit_token"} {
		body, _ = sjson.DeleteBytes(body, field)
	}
	return sjson.SetBytes(body, "model", modelID)
}

func setBedrockMantleHeaders(req *http.Request, stream bool, betaTokens []string) {
	req.Header.Set("Anthropic-Version", "2023-06-01")
	if len(betaTokens) > 0 {
		req.Header.Set("Anthropic-Beta", strings.Join(betaTokens, ","))
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
}

// Compatibility endpoints share the native body and authentication with Messages.
func (s *GatewayService) buildUpstreamRequestBedrockMantle(ctx context.Context, c *gin.Context, account *Account, body []byte, modelID string, stream bool) (*http.Request, []byte, error) {
	betaHeader := ""
	if c != nil && c.Request != nil {
		betaHeader = c.GetHeader("anthropic-beta")
	}
	betaTokens, err := s.resolveBedrockBetaTokensForRequest(ctx, account, betaHeader, body, modelID)
	if err != nil {
		return nil, nil, err
	}
	body, err = prepareBedrockMantleRequestBody(body, modelID, betaTokens)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare bedrock mantle request body: %w", err)
	}
	region := bedrockRuntimeRegion(account)
	if account.IsBedrockAPIKey() {
		apiKey := account.GetCredential("api_key")
		if apiKey == "" {
			return nil, nil, fmt.Errorf("api_key not found in bedrock credentials")
		}
		req, err := s.buildUpstreamRequestBedrockAPIKey(ctx, body, modelID, region, stream, apiKey, betaTokens...)
		return req, body, err
	}
	signer, err := NewBedrockSignerFromAccount(account)
	if err != nil {
		return nil, nil, fmt.Errorf("create bedrock signer: %w", err)
	}
	req, err := s.buildUpstreamRequestBedrock(ctx, body, modelID, region, stream, signer, betaTokens...)
	return req, body, err
}

func (s *GatewayService) handleBedrockMantleResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, model, upstreamModel string, stream bool, startTime time.Time) (*ForwardResult, error) {
	var usage *ClaudeUsage
	var firstTokenMs *int
	var clientDisconnect bool
	if stream {
		writerSizeBeforeStream := c.Writer.Size()
		result, err := s.handleStreamingResponse(ctx, resp, c, account, startTime, model, upstreamModel, false)
		if err != nil {
			var sseErr *sseStreamErrorEventError
			if errors.As(err, &sseErr) {
				return nil, s.handleAnthropicSSEError(ctx, c, account, resp, upstreamModel, writerSizeBeforeStream, sseErr)
			}
			return partialStreamUsageResult(c, resp, result, model, upstreamModel, startTime, err), err
		}
		usage, firstTokenMs, clientDisconnect = result.usage, result.firstTokenMs, result.clientDisconnect
	} else {
		var err error
		usage, err = s.handleNonStreamingResponse(ctx, resp, c, account, model, upstreamModel)
		if err != nil {
			return nil, err
		}
	}
	if usage == nil {
		usage = &ClaudeUsage{}
	}
	return &ForwardResult{
		RequestID:                     resp.Header.Get("x-request-id"),
		Usage:                         *usage,
		Model:                         model,
		UpstreamModel:                 upstreamModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		Stream:                        stream,
		Duration:                      time.Since(startTime),
		FirstTokenMs:                  firstTokenMs,
		ClientDisconnect:              clientDisconnect,
	}, nil
}

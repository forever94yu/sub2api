package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func vertexCountTokensLocationSupported(location string) bool {
	switch location {
	case "global", "us", "eu", "asia-southeast1":
		return true
	default:
		return false
	}
}

func (s *GatewayService) buildCountTokensRequestAnthropicVertex(ctx context.Context, c *gin.Context, account *Account, body []byte, token, modelID string) (*http.Request, []byte, error) {
	location := account.VertexLocation(modelID)
	if !vertexCountTokensLocationSupported(location) {
		return nil, nil, fmt.Errorf("count_tokens endpoint is not supported in Vertex location %s", location)
	}
	if strings.TrimSpace(modelID) == "" {
		return nil, nil, fmt.Errorf("vertex count_tokens model is required")
	}
	fullURL, err := buildVertexAnthropicURL(account.VertexProjectID(), location, "count-tokens", false)
	if err != nil {
		return nil, nil, err
	}
	vertexBody, err := buildVertexAnthropicRequestBody(sanitizeCountTokensRequestBody(body))
	if err != nil {
		return nil, nil, err
	}
	// Unlike message generation, Vertex token counting selects the model in the body.
	vertexBody = s.replaceModelInBody(vertexBody, normalizeVertexAnthropicModelID(modelID))

	clientBeta := ""
	if c != nil && c.Request != nil {
		clientBeta = getHeaderRaw(c.Request.Header, "anthropic-beta")
	}
	policy := s.evaluateBetaPolicy(ctx, clientBeta, account, modelID)
	if policy.blockErr != nil {
		return nil, nil, policy.blockErr
	}
	finalBeta := filterVertexBetaTokens(clientBeta, mergeDropSets(policy.filterSet))
	if sanitized, changed := sanitizeAnthropicBodyForBetaTokens(vertexBody, finalBeta); changed {
		vertexBody = sanitized
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(vertexBody))
	if err != nil {
		return nil, nil, err
	}
	// The US/EU multi-region endpoints use regional endpoint hosts in the Vertex SDK.
	if location == "us" || location == "eu" {
		req.URL.Host = "aiplatform." + location + ".rep.googleapis.com"
	}
	if c != nil && c.Request != nil {
		for key, values := range c.Request.Header {
			lowerKey := strings.ToLower(strings.TrimSpace(key))
			if !allowedHeaders[lowerKey] || lowerKey == "anthropic-version" || lowerKey == "anthropic-beta" {
				continue
			}
			for _, value := range values {
				addHeaderRaw(req.Header, resolveWireCasing(key), value)
			}
		}
	}
	for _, key := range []string{"authorization", "x-api-key", "x-goog-api-key", "cookie"} {
		deleteHeaderAllForms(req.Header, key)
	}
	setHeaderRaw(req.Header, "authorization", "Bearer "+token)
	setHeaderRaw(req.Header, "content-type", "application/json")
	if finalBeta != "" {
		setHeaderRaw(req.Header, "anthropic-beta", finalBeta)
	}
	return req, vertexBody, nil
}

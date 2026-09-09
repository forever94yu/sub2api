package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImage25NativeUsageWithoutOutputDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, details := range []struct {
			name   string
			fields string
			images int
		}{
			{name: "omitted", images: 50},
			{name: "explicit breakdown", fields: `,"output_tokens_details":{"image_tokens":40,"text_tokens":10}`, images: 40},
			{name: "explicit zero", fields: `,"output_tokens_details":{"image_tokens":0,"text_tokens":50}`, images: 0},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, details.name), func(t *testing.T) {
				usageJSON := `{"input_tokens":50,"output_tokens":50,"total_tokens":100,"input_tokens_details":{"text_tokens":10,"image_tokens":40}` + details.fields + `}`
				body := `{"created":1713833628,"data":[{"b64_json":"aW1hZ2U="}],"usage":` + usageJSON + `}`
				contentType := "application/json"
				if stream {
					body = "event: image_generation.completed\ndata: " + `{"type":"image_generation.completed","b64_json":"aW1hZ2U=","usage":` + usageJSON + "}\n\n"
					contentType = "text/event-stream"
				}
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
				svc := &OpenAIGatewayService{}
				var usage OpenAIUsage
				var count int
				var err error
				if stream {
					usage, count, _, _, err = svc.handleOpenAIImagesStreamingResponse(resp, c, time.Now())
				} else {
					usage, count, _, err = svc.handleOpenAIImagesNonStreamingResponse(resp, c)
				}
				require.NoError(t, err)
				require.Equal(t, 1, count)
				require.Equal(t, body, rec.Body.String(), "usage accounting must not rewrite the upstream response")
				require.Equal(t, 50, usage.InputTokens)
				require.Equal(t, 40, usage.ImageInputTokens)
				require.Equal(t, 50, usage.OutputTokens)
				require.Equal(t, details.images, usage.ImageOutputTokens)
				if details.name == "omitted" {
					for _, model := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare"} {
						cost := image25ParsedUsageCost(t, model, usage)
						require.InDelta(t, 0.0015, cost.ImageOutputCost, 1e-12)
						require.InDelta(t, 0.00187, cost.TotalCost, 1e-12)
					}
				}
			})
		}
	}
}

func TestImage25OAuthToolLocalInputBreakdown(t *testing.T) {
	svc := &OpenAIGatewayService{}
	for _, detail := range []struct {
		name   string
		fields string
		image  int
		cache  int
	}{
		{name: "tool local", fields: `,"input_tokens_details":{"image_tokens":80,"cached_tokens":10}`, image: 80, cache: 10},
		{name: "exponent notation", fields: `,"input_tokens_details":{"image_tokens":8e1,"cached_tokens":1e1}`, image: 80, cache: 10},
		{name: "absent"},
		{name: "invalid", fields: `,"input_tokens_details":{"image_tokens":-1,"cached_tokens":"10"}`},
		{name: "outside tool total", fields: `,"input_tokens_details":{"image_tokens":101,"cached_tokens":101}`},
		{name: "hostile exponent", fields: `,"input_tokens_details":{"image_tokens":1e1000000000,"cached_tokens":1e1000000000}`},
	} {
		t.Run(detail.name, func(t *testing.T) {
			payload := []byte(`{"type":"response.completed","response":{
				"usage":{"input_tokens":1000,"output_tokens":500,"input_tokens_details":{"image_tokens":700,"cached_tokens":900}},
				"tool_usage":{"image_gen":{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"image_tokens":50}` + detail.fields + `}}
			}}`)
			var usage OpenAIUsage
			svc.parseOpenAIImagesSSEUsageBytes(payload, &usage)
			require.Equal(t, OpenAIUsage{
				InputTokens: 100, OutputTokens: 50, ImageOutputTokens: 50,
				ImageInputTokens: detail.image, CacheReadInputTokens: detail.cache,
			}, usage)
			if detail.name == "tool local" {
				cost := image25ParsedUsageCost(t, "gpt-image-2.5-sunburst", usage)
				require.InDelta(t, 0.00064, cost.ImageInputCost, 1e-12)
				require.InDelta(t, 0.0000125, cost.CacheReadCost, 1e-12)
				require.InDelta(t, 0.0022025, cost.TotalCost, 1e-12)
			}
		})
	}
}

func image25ParsedUsageCost(t *testing.T, model string, usage OpenAIUsage) *CostBreakdown {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	pricing := &PricingService{}
	pricing.pricingData, err = pricing.parsePricingData(body)
	require.NoError(t, err)
	cost, err := NewBillingService(&config.Config{}, pricing).CalculateCost(model, UsageTokens{
		InputTokens:  usage.InputTokens - usage.CacheReadInputTokens,
		OutputTokens: usage.OutputTokens, ImageInputTokens: usage.ImageInputTokens,
		ImageOutputTokens: usage.ImageOutputTokens, CacheReadTokens: usage.CacheReadInputTokens,
	}, 1)
	require.NoError(t, err)
	return cost
}

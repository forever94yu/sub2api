package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestImage26NativeEditStreamCountsCompletedOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	partial := "event: image_edit.partial_image\ndata: " + `{"type":"image_edit.partial_image","partial_image_index":0,"b64_json":"cHJldmlldw==","size":"1024x1024"}` + "\n\n"
	completed := "event: image_edit.completed\ndata: " + `{"type":"image_edit.completed","b64_json":"ZmluYWw=","size":"1536x1024","usage":{"input_tokens":50,"input_tokens_details":{"text_tokens":10,"image_tokens":40},"output_tokens":100}}` + "\n\n"
	for _, tt := range []struct {
		name  string
		body  string
		count int
	}{
		{"partial only", partial, 0},
		{"completed", partial + completed, 1},
		{"duplicate completed", partial + completed + completed, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tt.body))}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, nil)
			usage, count, sizes, _, err := (&OpenAIGatewayService{}).handleOpenAIImagesStreamingResponse(response, c, time.Now())
			require.NoError(t, err)
			require.Equal(t, tt.count, count)
			require.Equal(t, tt.body, recorder.Body.String())
			if tt.count > 0 {
				require.Equal(t, []string{"1536x1024"}, sizes)
				require.Equal(t, 50, usage.InputTokens)
				require.Equal(t, 40, usage.ImageInputTokens)
				require.Equal(t, 100, usage.ImageOutputTokens)
			} else {
				require.Empty(t, sizes)
			}
		})
	}
}

func TestImage26SVGUsageSeparateFromRendering(t *testing.T) {
	body := []byte(`{"data":[{"b64_json":"PHN2Zy8+"}],"output_format":"svg","usage":{"input_tokens":50,"input_tokens_details":{"text_tokens":10,"image_tokens":40},"output_tokens":100},"svg_usage":{"model":"gpt-6.2-sol","input_tokens":200,"output_tokens":300,"reasoning_tokens":250,"total_tokens":500}}`)
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
	usage, count, _, err := (&OpenAIGatewayService{}).handleOpenAIImagesNonStreamingResponse(response, c)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, body, recorder.Body.Bytes())
	require.Equal(t, 50, usage.InputTokens)
	require.Equal(t, 40, usage.ImageInputTokens)
	require.Equal(t, 100, usage.OutputTokens)
	require.Equal(t, 100, usage.ImageOutputTokens)
	encoded, err := json.Marshal(usage)
	require.NoError(t, err)
	require.Equal(t, "gpt-6.2-sol", gjson.GetBytes(encoded, "svg_usage.model").String())
	require.Equal(t, int64(200), gjson.GetBytes(encoded, "svg_usage.input_tokens").Int())
	require.Equal(t, int64(300), gjson.GetBytes(encoded, "svg_usage.output_tokens").Int(), "reasoning is already included in output tokens")
}

func TestImage26SVGUsageInvalidCountsAreNotBilled(t *testing.T) {
	for _, svg := range []string{
		`{"model":"","input_tokens":200,"output_tokens":300}`,
		`{"model":"gpt-6.2-sol","input_tokens":-1,"output_tokens":300}`,
		`{"model":"gpt-6.2-sol","input_tokens":"200","output_tokens":300}`,
		`{"model":"gpt-6.2-sol","input_tokens":1e1000000000,"output_tokens":300}`,
		`{"model":"gpt-6.2-sol","input_tokens":200,"output_tokens":0.5}`,
	} {
		t.Run(svg, func(t *testing.T) {
			usage, ok := extractOpenAIImagesUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":50,"output_tokens":100},"svg_usage":` + svg + `}`))
			require.True(t, ok)
			encoded, err := json.Marshal(usage)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(encoded, "svg_usage").Exists())
		})
	}
}

func TestImage26NativeOptionsDoNotSilentlyCrossResponsesBridge(t *testing.T) {
	for _, option := range []string{
		`"complexity":"low"`,
		`"svg":{"model":"gpt-6.2-sol"}`,
		`"frame_count":9`,
		`"frame_duration_ms":200`,
		`"output_format":"svg"`,
		`"output_format":"gif"`,
	} {
		t.Run(option, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2.6-sunburst","prompt":"a geometric logo",` + option + `}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.True(t, parsed.HasNativeOptions)
			require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapability)
			rewritten, _, err := rewriteOpenAIImagesModel(body, parsed.ContentType, "gpt-image-2.6-flare")
			require.NoError(t, err)
			require.JSONEq(t, strings.Replace(string(body), "gpt-image-2.6-sunburst", "gpt-image-2.6-flare", 1), string(rewritten))
			_, err = buildOpenAIImagesResponsesRequest(parsed, parsed.Model)
			require.ErrorContains(t, err, "native Images API")
		})
	}
}

func TestImage26MultipartMarkupIsNativeOnlyAndPreserved(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2.6-flare"))
	require.NoError(t, writer.WriteField("prompt", "change the marked area"))
	for _, name := range []string{"image", "markup"} {
		part, err := writer.CreateFormFile(name, name+".png")
		require.NoError(t, err)
		_, err = io.WriteString(part, "bytes-for-"+name)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)
	require.True(t, parsed.HasNativeOptions)
	_, err = buildOpenAIImagesResponsesRequest(parsed, parsed.Model)
	require.ErrorContains(t, err, "native Images API")
	rewritten, contentType, err := rewriteOpenAIImagesModel(body.Bytes(), parsed.ContentType, "gpt-image-2.6-sunburst")
	require.NoError(t, err)
	forwarded := httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(rewritten))
	forwarded.Header.Set("Content-Type", contentType)
	require.NoError(t, forwarded.ParseMultipartForm(1<<20))
	file, header, err := forwarded.FormFile("markup")
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, "markup.png", header.Filename)
	require.Equal(t, "bytes-for-markup", string(data))
}

func TestImage26OutputMIMETypes(t *testing.T) {
	require.Equal(t, "image/svg+xml", openAIImageOutputMIMEType("svg"))
	require.Equal(t, "image/gif", openAIImageOutputMIMEType("gif"))
}

func TestImage26NativeOptionsSelectAPIKeyAccounts(t *testing.T) {
	capability := OpenAIImagesCapabilityAPIKey
	require.True(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).SupportsOpenAIImageCapability(capability))
	require.False(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).SupportsOpenAIImageCapability(capability))
}

func TestImage26OAuthNativeOnlyErrorReachesClient(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2.6-sunburst", Prompt: "logo", OutputFormat: "svg", N: 1}
	result, err := (&OpenAIGatewayService{}).ForwardImages(context.Background(), c,
		&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, nil, parsed, "")
	require.Nil(t, result)
	require.ErrorContains(t, err, "native Images API")
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "native Images API")
}

func TestImage26SVGObjectStoragePreservesFormatAndUsage(t *testing.T) {
	svg := []byte(`<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M0 0L16 16"/></svg>`)
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	response := []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(svg) + `"}],"svg_usage":{"model":"gpt-6.2-sol","input_tokens":200,"output_tokens":300}}`)
	result, err := uploader.Rewrite(context.Background(), "image26", response)
	require.NoError(t, err)
	require.Len(t, storage.saved, 1)
	require.Equal(t, "images/image26-0.svg", storage.saved[0].key)
	require.Equal(t, "image/svg+xml", storage.saved[0].contentType)
	require.Equal(t, svg, storage.saved[0].data)
	require.Equal(t, "gpt-6.2-sol", gjson.GetBytes(result, "svg_usage.model").String())
	require.Equal(t, "https://cdn.test/images/image26-0.svg", gjson.GetBytes(result, "data.0.url").String())
	for _, body := range []string{`<html><svg/></html>`, `<not_svg/>`, `<svg xmlns="urn:not-svg"/>`, `plain text`} {
		require.Empty(t, detectedImageContentType([]byte(body)), "must identify an SVG root rather than a text substring")
	}
}

package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAllowOpenAICompatibleMessagesDispatch_GrokExempt(t *testing.T) {
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, nil), "无 key 保持放行")

	grok := &service.APIKey{Group: &service.Group{Platform: service.PlatformGrok, AllowMessagesDispatch: false}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, grok))

	// 非回归：openai 分组仍受开关控制。
	openaiOff := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: false}}
	require.False(t, allowOpenAICompatibleMessagesDispatch(nil, openaiOff))
	openaiOn := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: true}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, openaiOn))
}

func TestAllowOpenAICompatibleMessagesDispatch_CompositeResolvedTargets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCompositeCtx := func(model string) (*gin.Context, *service.APIKey) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false}}
		ensureCompositeTargetPlatform(c, apiKey, model)
		return c, apiKey
	}

	// 解析到 Grok 目标时与对应独立分组同语义豁免。
	cGrok, grokKey := newCompositeCtx("grok-4.3")
	require.True(t, allowOpenAICompatibleMessagesDispatch(cGrok, grokKey))

	for _, model := range []string{"kimi-k2-thinking", "glm-5.2", "deepseek-v3.2"} {
		c, apiKey := newCompositeCtx(model)
		require.False(t, allowOpenAICompatibleMessagesDispatch(c, apiKey), "model=%s", model)
		_, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		require.False(t, ok, "model=%s", model)
	}

	// 解析到 openai 目标：仍受开关控制（composite 被 sanitize 恒置 false ⇒ 拒绝）。
	c, apiKey := newCompositeCtx("gpt-5.5")
	require.False(t, allowOpenAICompatibleMessagesDispatch(c, apiKey))

	// 未解析出目标平台：保持拒绝，不放宽。
	cNone, _ := gin.CreateTestContext(httptest.NewRecorder())
	cNone.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	require.False(t, allowOpenAICompatibleMessagesDispatch(cNone,
		&service.APIKey{Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false}}))
}

// composite 解析到 Grok 目标时，Group 级调度映射（gpt-5.x 默认值为 OpenAI
// 专属）不得注入，模型改写完全交给账号级 model_mapping。
func TestResolveOpenAIMessagesDispatchMappedModel_CompositeGrokTargetSkipsGroupMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}
	ensureCompositeTargetPlatform(c, apiKey, "grok-4.3")

	require.Empty(t, resolveOpenAIMessagesDispatchMappedModel(c, apiKey, "claude-sonnet-4-5-20250929"))
}

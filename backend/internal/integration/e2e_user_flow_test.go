//go:build e2e

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// E2E 用户流程测试
// 测试完整的用户操作链路：注册 → 登录 → 创建 API Key → 调用网关 → 查询用量

var (
	testUserEmail    = "e2e-test-" + fmt.Sprintf("%d", time.Now().UnixMilli()) + "@test.local"
	testUserPassword = "E2eTest@12345"
	testUserName     = "e2e-test-user"
)

const panelAPIPrefix = "/api/v1"

// TestUserRegistrationAndLogin 测试用户注册和登录流程
func TestUserRegistrationAndLogin(t *testing.T) {
	registrationDisabled := false

	// 步骤 1: 注册新用户
	t.Run("注册新用户", func(t *testing.T) {
		payload := map[string]string{
			"email":    testUserEmail,
			"password": testUserPassword,
			"username": testUserName,
		}
		body, _ := json.Marshal(payload)

		resp, err := doRequest(t, "POST", panelAPIPrefix+"/auth/register", body, "")
		if err != nil {
			t.Fatalf("注册请求失败: %v", err)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)

		// 注册关闭是允许的部署配置，其余非成功状态都表示流程或接口异常。
		switch resp.StatusCode {
		case 200:
			t.Logf("✅ 用户注册成功: %s", testUserEmail)
		case 403:
			if !strings.Contains(string(respBody), "REGISTRATION_DISABLED") {
				t.Fatalf("注册返回意外的 HTTP 403: %s", string(respBody))
			}
			registrationDisabled = true
			t.Logf("注册功能已关闭: %s", string(respBody))
		default:
			t.Fatalf("注册返回意外状态 HTTP %d: %s", resp.StatusCode, string(respBody))
		}
	})
	if registrationDisabled {
		t.Skip("注册功能已关闭，跳过需要新用户的注册登录流程")
	}

	// 步骤 2: 登录获取 JWT
	var accessToken string
	t.Run("用户登录获取JWT", func(t *testing.T) {
		payload := map[string]string{
			"email":    testUserEmail,
			"password": testUserPassword,
		}
		body, _ := json.Marshal(payload)

		resp, err := doRequest(t, "POST", panelAPIPrefix+"/auth/login", body, "")
		if err != nil {
			t.Fatalf("登录请求失败: %v", err)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Fatalf("登录失败 HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		var result map[string]any
		if err := json.Unmarshal(respBody, &result); err != nil {
			t.Fatalf("解析登录响应失败: %v", err)
		}

		// 尝试从标准响应格式获取 token
		if token, ok := result["access_token"].(string); ok && token != "" {
			accessToken = token
		} else if data, ok := result["data"].(map[string]any); ok {
			if token, ok := data["access_token"].(string); ok {
				accessToken = token
			}
		}

		if accessToken == "" {
			t.Fatalf("未获取到 access_token，响应: %s", string(respBody))
		}

		// 验证 token 不为空且格式基本正确
		if len(accessToken) < 10 {
			t.Fatalf("access_token 格式异常: %s", accessToken)
		}

		t.Logf("✅ 登录成功，获取 JWT（长度: %d）", len(accessToken))
	})

	// 步骤 3: 使用 JWT 获取当前用户信息
	t.Run("获取当前用户信息", func(t *testing.T) {
		resp, err := doRequest(t, "GET", panelAPIPrefix+"/user/profile", nil, accessToken)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		t.Logf("✅ 成功获取用户信息")
	})
}

// TestAPIKeyLifecycle 测试 API Key 的创建和使用
func TestAPIKeyLifecycle(t *testing.T) {
	// 先登录获取 JWT
	accessToken := loginTestUser(t)

	var apiKey string

	// 步骤 1: 创建 API Key
	t.Run("创建API_Key", func(t *testing.T) {
		payload := map[string]string{
			"name": "e2e-test-key-" + fmt.Sprintf("%d", time.Now().UnixMilli()),
		}
		body, _ := json.Marshal(payload)

		resp, err := doRequest(t, "POST", panelAPIPrefix+"/keys", body, accessToken)
		if err != nil {
			t.Fatalf("创建 API Key 请求失败: %v", err)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Fatalf("创建 API Key 失败 HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		var result map[string]any
		if err := json.Unmarshal(respBody, &result); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}

		// 从响应中提取 key
		if key, ok := result["key"].(string); ok {
			apiKey = key
		} else if data, ok := result["data"].(map[string]any); ok {
			if key, ok := data["key"].(string); ok {
				apiKey = key
			}
		}

		if apiKey == "" {
			t.Fatalf("未获取到 API Key，响应: %s", string(respBody))
		}

		t.Logf("✅ API Key 创建成功: %s", redactAPIKey(apiKey))
	})

	// 步骤 2: 使用 API Key 调用网关（需要 Claude 或 Gemini 可用）
	t.Run("使用API_Key调用网关", func(t *testing.T) {
		// 尝试调用 models 列表（最轻量的 API 调用）
		resp, err := doRequest(t, "GET", "/v1/models", nil, apiKey)
		if err != nil {
			t.Fatalf("网关请求失败: %v", err)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)

		// 可能返回 200（成功）或 402（余额不足）或 403（无可用账户）
		switch {
		case resp.StatusCode == 200:
			t.Logf("✅ API Key 网关调用成功")
		case resp.StatusCode == 402:
			t.Logf("⚠️ 余额不足，但 API Key 认证通过")
		case resp.StatusCode == 403:
			t.Logf("⚠️ 无可用账户，但 API Key 认证通过")
		default:
			t.Fatalf("网关返回意外状态 HTTP %d: %s", resp.StatusCode, string(respBody))
		}
	})

	// 步骤 3: 查询用量记录
	t.Run("查询用量记录", func(t *testing.T) {
		resp, err := doRequest(t, "GET", panelAPIPrefix+"/usage/dashboard/stats", nil, accessToken)
		if err != nil {
			t.Fatalf("用量查询请求失败: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("用量查询返回 HTTP %d: %s", resp.StatusCode, string(body))
		}

		t.Logf("✅ 用量查询成功")
	})
}

// =============================================================================
// 辅助函数
// =============================================================================

func doRequest(t *testing.T, method, path string, body []byte, token string) (*http.Response, error) {
	t.Helper()

	url := baseURL + path
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

func loginTestUser(t *testing.T) string {
	t.Helper()

	// 先尝试用管理员账户登录
	adminEmail := getEnv("ADMIN_EMAIL", "admin@sub2api.local")
	adminPassword := getEnv("ADMIN_PASSWORD", "")

	if adminPassword == "" {
		// 尝试用测试用户
		adminEmail = testUserEmail
		adminPassword = testUserPassword
	}

	payload := map[string]string{
		"email":    adminEmail,
		"password": adminPassword,
	}
	body, _ := json.Marshal(payload)

	resp, err := doRequest(t, "POST", panelAPIPrefix+"/auth/login", body, "")
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		if adminPassword == "" && resp.StatusCode == http.StatusUnauthorized {
			t.Skipf("未配置 ADMIN_PASSWORD，且测试用户不可登录: %s", string(respBody))
		}
		t.Fatalf("登录失败 HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		t.Fatalf("解析登录响应失败: %v", err)
	}

	if token, ok := result["access_token"].(string); ok {
		return token
	}
	if data, ok := result["data"].(map[string]any); ok {
		if token, ok := data["access_token"].(string); ok {
			return token
		}
	}

	t.Fatalf("登录响应缺少 access_token: %s", string(respBody))
	return ""
}

// redactAPIKey API Key 脱敏，只显示前 8 位
func redactAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "***"
	}
	return key[:8] + "..."
}

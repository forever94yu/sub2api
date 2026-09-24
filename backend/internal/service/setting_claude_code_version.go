package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

type cachedClaudeCodeClientVersion struct {
	version    string
	expiresAt  int64
	generation uint64
}

const (
	claudeCodeClientVersionCacheTTL  = 60 * time.Second
	claudeCodeClientVersionErrorTTL  = 5 * time.Second
	claudeCodeClientVersionDBTimeout = 5 * time.Second
	claudeCodeClientVersionSFKey     = "claude_code_client_version"
)

// NormalizeClaudeCodeClientVersion 校验并归一化 Claude Code 客户端版本号，非法值返回空串。
// 容忍前导 "v" 与首尾空白；合法性复用 claude.IsSupportedCLIVersion（严格三段纯数字 semver、
// 无预发布/构建后缀、且不低于内置基线）。
func NormalizeClaudeCodeClientVersion(version string) string {
	normalized := strings.TrimSpace(version)
	normalized = strings.TrimPrefix(normalized, "v")
	if normalized == "" {
		return ""
	}
	if !claude.IsSupportedCLIVersion(normalized) {
		return ""
	}
	return normalized
}

// GetClaudeCodeClientVersion 返回出站声明的 Claude Code CLI 客户端版本号。
// 优先级：管理员在面板覆写的版本 → 自动同步到的官方最新版本 → claude.CLIVersion()
// （环境变量 SUB2API_CLAUDE_CLI_VERSION 覆盖 + 内置基线）。
// 版本太旧会被 Anthropic 拒绝（HTTP 400 claude_code_version_too_old），故该值需保持跟随官方发布。
//
// ⚠️ 一致性约束：同一次请求里，拼 User-Agent（claude-cli/<版本>）的版本号和用于
// billing attribution 的版本号必须是同一个值，否则 Anthropic 侧对不上、判为非正版客户端。
// 因此调用点必须在一次请求内只取一次本函数并复用；本缓存的唯一主动变更点是
// 同步任务写入后调用 InvalidateClaudeCodeClientVersionCache，60s TTL 不会在极短时间内抖动。
func (s *SettingService) GetClaudeCodeClientVersion(ctx context.Context) string {
	fallback := claude.CLIVersion()
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	generation := s.claudeCodeVersionGeneration.Load()
	if cached, ok := s.claudeCodeVersionCache.Load().(*cachedClaudeCodeClientVersion); ok && cached != nil {
		if cached.generation == generation && time.Now().UnixNano() < cached.expiresAt {
			return cached.version
		}
	}

	result, _, _ := s.claudeCodeVersionSF.Do(claudeCodeClientVersionSFKey, func() (any, error) {
		if cached, ok := s.claudeCodeVersionCache.Load().(*cachedClaudeCodeClientVersion); ok && cached != nil {
			if cached.generation == generation && time.Now().UnixNano() < cached.expiresAt {
				return cached.version, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claudeCodeClientVersionDBTimeout)
		defer cancel()
		values, err := s.settingRepo.GetMultiple(dbCtx, []string{
			SettingKeyClaudeCodeClientVersion,
			SettingKeyClaudeCodeClientVersionSynced,
		})
		if err != nil {
			slog.Warn("failed to get claude code client version setting", "error", err)
			s.claudeCodeVersionCache.Store(&cachedClaudeCodeClientVersion{
				version:    fallback,
				expiresAt:  time.Now().Add(claudeCodeClientVersionErrorTTL).UnixNano(),
				generation: generation,
			})
			return fallback, nil
		}
		version := NormalizeClaudeCodeClientVersion(values[SettingKeyClaudeCodeClientVersion])
		if version == "" {
			if raw := values[SettingKeyClaudeCodeClientVersion]; strings.TrimSpace(raw) != "" {
				slog.Warn("ignoring invalid claude_code_client_version setting; falling back to the next layer",
					"value", raw)
			}
			version = NormalizeClaudeCodeClientVersion(values[SettingKeyClaudeCodeClientVersionSynced])
			if version == "" && strings.TrimSpace(values[SettingKeyClaudeCodeClientVersionSynced]) != "" {
				slog.Warn("ignoring invalid claude_code_client_version_synced setting; falling back to the built-in pin",
					"value", values[SettingKeyClaudeCodeClientVersionSynced])
			}
		}
		if version == "" {
			version = fallback
		}
		s.claudeCodeVersionCache.Store(&cachedClaudeCodeClientVersion{
			version:    version,
			expiresAt:  time.Now().Add(claudeCodeClientVersionCacheTTL).UnixNano(),
			generation: generation,
		})
		return version, nil
	})
	if version, ok := result.(string); ok && version != "" {
		return version
	}
	return fallback
}

// InvalidateClaudeCodeClientVersionCache 丢弃版本号缓存，下次读取回源。
// 面板保存与自动同步写入后调用。
func (s *SettingService) InvalidateClaudeCodeClientVersionCache() {
	if s == nil {
		return
	}
	// In-flight reads may finish after invalidation; their cache entries stay stale.
	s.claudeCodeVersionGeneration.Add(1)
	s.claudeCodeVersionSF.Forget(claudeCodeClientVersionSFKey)
	s.claudeCodeVersionCache.Store((*cachedClaudeCodeClientVersion)(nil))
}

package domain

import "time"

// 渠道监控「配额模式」的归一化配额快照类型。
//
// 配额模式监控不直接对接上游，而是关联一个已有账号，复用账号侧的用量服务，
// 把各平台形态各异的用量数据归一成 MonitorQuotaSnapshot，随检测历史持久化
// 到 channel_monitor_histories.quota（JSONB），供管理端与用户端渲染。
//
// 类型放在 domain 包是因为 ent schema（internal/domain 的下游）需要引用它做
// field.JSON 序列化；service 不能被 ent import（会造成循环依赖）。

// MonitorQuotaTier 单个用量窗口的快照。
//
// Window 取值约定（与前端 monitorCommon.quota.windows.* 标签一一对应）：
//   - "5h"         5 小时滚动窗口（Claude/Codex）
//   - "7d"         7 天窗口（Claude/Codex）
//   - "7d-sonnet"  Claude 7 天 Sonnet 独立额度
//   - "7d-fable"   Claude 7 天 Fable 独立额度
//   - "daily"      日窗口（Gemini 日配额 / Grok 日请求）
//   - "30d"        30 天窗口（Grok 月度）
//   - "total"      无窗口语义的总量额度（Antigravity per-model 等）
//
// 同一 Window 可能出现多条（Gemini 多档日配额、Antigravity per-model、
// Grok requests/tokens），用 Label 区分：Label 是机器 token（requests/tokens/
// shared/pro/flash 或模型名），前端已知 token 走 i18n，未知原样展示。
type MonitorQuotaTier struct {
	Window      string  `json:"window"`
	Label       string  `json:"label,omitempty"`
	UsedPercent float64 `json:"used_percent"` // 0-100+；仅有绝对值时按 used/limit 计算
	Used        float64 `json:"used,omitempty"`
	Limit       float64 `json:"limit,omitempty"`
	ResetAt     string  `json:"reset_at,omitempty"` // RFC3339；未知时留空
}

// MonitorQuotaSnapshot 一次配额查询的完整快照。
//
// Source 当前固定为 "usage"，数据来自 AccountUsageService.GetUsage。
type MonitorQuotaSnapshot struct {
	Source    string             `json:"source"`
	Success   bool               `json:"success"`
	Tiers     []MonitorQuotaTier `json:"tiers,omitempty"`
	PlanLevel string             `json:"plan_level,omitempty"` // 上游套餐等级
	// CredentialInvalid 上游 401/403 鉴权失败（区别于网络/解析错误），
	// 检测状态据此推导 failed 而非 error。
	CredentialInvalid bool      `json:"credential_invalid,omitempty"`
	Error             string    `json:"error,omitempty"` // Success=false 时的错误摘要
	FetchedAt         time.Time `json:"fetched_at"`
}

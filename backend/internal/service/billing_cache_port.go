package service

import (
	"time"
)

// SubscriptionCacheData represents cached subscription data
type SubscriptionCacheData struct {
	Status    string
	ExpiresAt time.Time
	// FiveHourWindowStart 为 nil 表示 5 小时窗口尚未开启（或缓存来自旧版本）。
	FiveHourWindowStart *time.Time
	FiveHourUsage       float64
	DailyUsage          float64
	WeeklyUsage         float64
	MonthlyUsage        float64
	Version             int64
}

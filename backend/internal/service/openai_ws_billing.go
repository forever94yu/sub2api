package service

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
)

var ErrWebSocketAuthentication = errors.New("websocket authentication is no longer valid; reconnect required")

// RefreshWebSocketAPIKey bypasses auth caches and keeps the connection bound to
// its original owner and group. GetByKey also loads allowed groups and usage
// windows, which are absent from the smaller ID/auth projections.
func (s *APIKeyService) RefreshWebSocketAPIKey(ctx context.Context, initial *APIKey, clientIP string) (*APIKey, error) {
	if s == nil || s.apiKeyRepo == nil || initial == nil {
		return nil, ErrWebSocketAuthentication
	}
	key, err := s.apiKeyRepo.GetByKey(ctx, initial.Key)
	if err != nil {
		return nil, err
	}
	if key == nil || key.ID != initial.ID || key.UserID != initial.UserID || key.User == nil || key.User.ID != key.UserID || !key.User.IsActive() {
		return nil, ErrWebSocketAuthentication
	}
	if !key.IsActive() && key.Status != StatusAPIKeyExpired && key.Status != StatusAPIKeyQuotaExhausted {
		return nil, ErrWebSocketAuthentication
	}
	if (key.GroupID == nil) != (initial.GroupID == nil) ||
		(key.GroupID != nil && *key.GroupID != *initial.GroupID) {
		return nil, ErrWebSocketAuthentication
	}
	if key.GroupID != nil {
		if key.Group == nil || key.Group.ID != *key.GroupID || !key.Group.IsActive() {
			return nil, ErrWebSocketAuthentication
		}
		if !key.Group.IsSubscriptionType() && !key.User.CanBindGroup(key.Group.ID, key.Group.IsExclusive) {
			return nil, ErrWebSocketAuthentication
		}
	}
	s.compileAPIKeyIPRules(key)
	if len(key.IPWhitelist) > 0 || len(key.IPBlacklist) > 0 {
		if allowed, _ := ip.CheckIPRestrictionWithCompiledRules(clientIP, key.CompiledIPWhitelist, key.CompiledIPBlacklist); !allowed {
			return nil, ErrWebSocketAuthentication
		}
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return key, nil
	}
	if key.Status == StatusAPIKeyExpired || key.IsExpired() {
		return nil, ErrAPIKeyExpired
	}
	if key.Status == StatusAPIKeyQuotaExhausted || key.IsQuotaExhausted() {
		return nil, ErrAPIKeyQuotaExhausted
	}
	return key, nil
}

// CheckWebSocketBillingEligibility runs only after the preceding turn's billing
// transaction completes. key must be an uncached snapshot from the repository.
func (s *BillingCacheService) CheckWebSocketBillingEligibility(ctx context.Context, key *APIKey, platform string) (*UserSubscription, error) {
	if s == nil || key == nil || key.User == nil {
		return nil, ErrBillingServiceUnavailable
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.circuitBreaker != nil && !s.circuitBreaker.Allow() {
		return nil, ErrBillingServiceUnavailable
	}
	var subscription *UserSubscription
	if key.Group != nil && key.Group.IsSubscriptionType() {
		if s.subRepo == nil {
			return nil, ErrBillingServiceUnavailable
		}
		var err error
		subscription, err = s.subRepo.GetActiveByUserIDAndGroupID(ctx, key.User.ID, key.Group.ID)
		if err != nil {
			if s.circuitBreaker != nil {
				s.circuitBreaker.OnFailure(err)
			}
			return nil, err
		}
		if s.circuitBreaker != nil {
			s.circuitBreaker.OnSuccess()
		}
		if subscription == nil || !subscription.IsActive() {
			return nil, ErrSubscriptionInvalid
		}
		// Reuse the conditional window updates and database reread used by HTTP
		// authentication, without creating another cache or maintenance worker.
		maintenance := &SubscriptionService{userSubRepo: s.subRepo, billingCacheService: s, now: time.Now}
		needsMaintenance, err := maintenance.ValidateAndCheckLimits(subscription, key.Group)
		if needsMaintenance {
			subscription, err = maintenance.EnsureWindowMaintenance(ctx, subscription)
			if err != nil {
				return nil, err
			}
			if subscription == nil || !subscription.IsActive() {
				return nil, ErrSubscriptionInvalid
			}
			_, err = maintenance.ValidateAndCheckLimits(subscription, key.Group)
		}
		if err != nil {
			return nil, err
		}
		// ValidateAndCheckLimits accepts usage == limit for quota updates;
		// admitting another billable request requires remaining headroom.
		if key.Group.HasDailyLimit() && subscription.DailyUsageUSD >= *key.Group.DailyLimitUSD {
			return nil, ErrDailyLimitExceeded
		}
		if key.Group.HasWeeklyLimit() && subscription.WeeklyUsageUSD >= *key.Group.WeeklyLimitUSD {
			return nil, ErrWeeklyLimitExceeded
		}
		if key.Group.HasMonthlyLimit() && subscription.MonthlyUsageUSD >= *key.Group.MonthlyLimitUSD {
			return nil, ErrMonthlyLimitExceeded
		}
	} else {
		if s.circuitBreaker != nil {
			s.circuitBreaker.OnSuccess()
		}
		if s.balanceBelowEligibilityThreshold(key.User.Balance) {
			return nil, ErrInsufficientBalance
		}
		// Platform quotas are updated synchronously in Redis by RecordUsage;
		// their database counters may intentionally lag behind the flusher.
		if err := s.checkUserPlatformQuotaEligibility(ctx, key.User.ID, platform); err != nil {
			return nil, err
		}
	}
	if key.HasRateLimits() {
		if err := s.evaluateRateLimits(ctx, key, key.Usage5h, key.Usage1d, key.Usage7d,
			key.Window5hStart, key.Window1dStart, key.Window7dStart); err != nil {
			return nil, err
		}
	}
	if err := s.checkRPM(ctx, key.User, key.Group); err != nil {
		return nil, err
	}
	return subscription, nil
}

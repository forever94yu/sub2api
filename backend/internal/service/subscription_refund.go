package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// The removed interval allows rollback and later pending-refund settlement to
// preserve renewals without restoring an old subscription row snapshot.
type subscriptionRefundAdjustment struct {
	SubscriptionID int64     `json:"subscriptionID"`
	BeforeExpiry   time.Time `json:"beforeExpiry"`
	AfterExpiry    time.Time `json:"afterExpiry"`
	BeforeStatus   string    `json:"beforeStatus"`
	AfterStatus    string    `json:"afterStatus"`
}

func (a *subscriptionRefundAdjustment) remaining(now time.Time) time.Duration {
	start := a.AfterExpiry
	if now.After(start) {
		start = now
	}
	if !a.BeforeExpiry.After(start) {
		return 0
	}
	return a.BeforeExpiry.Sub(start)
}

func (s *SubscriptionService) deductRefundSubscription(ctx context.Context, id int64, days int, previous *subscriptionRefundAdjustment) (*subscriptionRefundAdjustment, error) {
	var adjustment *subscriptionRefundAdjustment
	err := s.withSubscriptionUpdateTx(ctx, func(txCtx context.Context) error {
		sub, err := s.userSubRepo.GetByIDForUpdate(txCtx, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		if s.now != nil {
			now = s.now().UTC().Truncate(time.Microsecond)
		}
		if !sub.ExpiresAt.After(now) || days <= 0 {
			return nil
		}
		if days > MaxValidityDays {
			days = MaxValidityDays
		}
		expiresAt := sub.ExpiresAt.AddDate(0, 0, -days)
		if previous != nil {
			remaining := previous.remaining(now)
			if remaining <= 0 {
				return nil
			}
			expiresAt = sub.ExpiresAt.Add(-remaining)
		}
		if expiresAt.Before(now) {
			expiresAt = now
		}
		status := sub.Status
		if !expiresAt.After(now) {
			status = SubscriptionStatusExpired
		}
		adjustment = &subscriptionRefundAdjustment{
			SubscriptionID: id, BeforeExpiry: sub.ExpiresAt, AfterExpiry: expiresAt,
			BeforeStatus: sub.Status, AfterStatus: status,
		}
		if err := s.userSubRepo.ExtendExpiry(txCtx, id, expiresAt); err != nil {
			return err
		}
		if status != sub.Status {
			if err := s.userSubRepo.UpdateStatus(txCtx, id, status); err != nil {
				return err
			}
		}
		s.invalidateSubscriptionAfterCommit(txCtx, sub.UserID, sub.GroupID)
		return nil
	})
	return adjustment, err
}

func (s *SubscriptionService) rollbackRefundSubscription(ctx context.Context, adjustment *subscriptionRefundAdjustment) error {
	return s.withSubscriptionUpdateTx(ctx, func(txCtx context.Context) error {
		sub, err := s.userSubRepo.GetByIDForUpdate(txCtx, adjustment.SubscriptionID)
		if err != nil {
			// A separate admin revocation must never be undone by refund rollback.
			return err
		}
		now := time.Now().UTC()
		if s.now != nil {
			now = s.now().UTC()
		}
		expiresAt := adjustment.BeforeExpiry
		if !sub.ExpiresAt.Equal(adjustment.AfterExpiry) {
			expiresAt = sub.ExpiresAt.Add(adjustment.remaining(now))
		}
		if expiresAt.After(MaxExpiresAt) {
			expiresAt = MaxExpiresAt
		}
		if err := s.userSubRepo.ExtendExpiry(txCtx, sub.ID, expiresAt); err != nil {
			return err
		}
		if sub.Status == adjustment.AfterStatus && sub.ExpiresAt.Equal(adjustment.AfterExpiry) {
			status := adjustment.BeforeStatus
			if status == SubscriptionStatusActive && !expiresAt.After(now) {
				status = SubscriptionStatusExpired
			}
			if err := s.userSubRepo.UpdateStatus(txCtx, sub.ID, status); err != nil {
				return err
			}
		}
		s.invalidateSubscriptionAfterCommit(txCtx, sub.UserID, sub.GroupID)
		return nil
	})
}

func (s *SubscriptionService) invalidateSubscriptionAfterCommit(ctx context.Context, userID, groupID int64) {
	invalidate := func() {
		if err := s.invalidateSubscriptionCaches(userID, groupID); err != nil {
			slog.Error("invalidate refund subscription cache", "userID", userID, "groupID", groupID, "error", err)
		}
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		tx.OnCommit(func(next dbent.Committer) dbent.Committer {
			return dbent.CommitFunc(func(commitCtx context.Context, tx *dbent.Tx) error {
				if err := next.Commit(commitCtx, tx); err != nil {
					return fmt.Errorf("commit subscription refund: %w", err)
				}
				invalidate()
				return nil
			})
		})
		return
	}
	invalidate()
}

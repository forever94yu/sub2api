package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReduceSubscriptionPreservesPartialDay(t *testing.T) {
	for _, tc := range []struct {
		name           string
		remainingHours int
		wantHours      int
		wantStatus     string
	}{
		{name: "partial day remains", remainingHours: 47, wantHours: 23, wantStatus: SubscriptionStatusActive},
		{name: "whole days remain", remainingHours: 72, wantHours: 48, wantStatus: SubscriptionStatusActive},
		{name: "remaining term exhausted", remainingHours: 23, wantHours: 0, wantStatus: SubscriptionStatusExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Microsecond)
			sub := UserSubscription{ID: 7, UserID: 11, GroupID: 13, Status: SubscriptionStatusActive,
				ExpiresAt: now.Add(time.Duration(tc.remainingHours) * time.Hour)}
			repo := &lockingRenewalRepo{stale: sub, current: sub}
			svc := &RedeemService{subscriptionService: &SubscriptionService{userSubRepo: repo}}

			require.NoError(t, svc.reduceOrCancelSubscription(context.Background(), sub.UserID, sub.GroupID, 1, "REFUND-ONE-DAY"))
			require.Equal(t, tc.wantStatus, repo.current.Status)
			if tc.wantHours == 0 {
				require.WithinDuration(t, now, repo.current.ExpiresAt, time.Second)
			} else {
				require.Equal(t, now.Add(time.Duration(tc.wantHours)*time.Hour), repo.current.ExpiresAt)
			}
		})
	}
}

//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestApplyUsageBillingEffects_DeletedKeyDoesNotCancelCharge(t *testing.T) {
	for _, tc := range []struct {
		name  string
		quota bool
		rate  bool
	}{
		{name: "quota", quota: true},
		{name: "rate", rate: true},
		{name: "quota_and_rate", quota: true, rate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			tx, err := db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			mock.ExpectQuery(conditionalBalanceDeductSQL).
				WithArgs(10.0, int64(42)).
				WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(90.0))
			cmd := &service.UsageBillingCommand{UserID: 42, APIKeyID: 7, BalanceCost: 10}
			if tc.quota {
				cmd.APIKeyQuotaCost = 10
				mock.ExpectQuery(`(?s)UPDATE api_keys.*SET quota_used.*WHERE id = \$2 AND deleted_at IS NULL`).
					WithArgs(10.0, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
					WillReturnError(sql.ErrNoRows)
			}
			if tc.rate {
				cmd.APIKeyRateLimitCost = 10
				mock.ExpectExec(`(?s)UPDATE api_keys SET.*usage_5h.*WHERE id = \$2 AND deleted_at IS NULL`).
					WithArgs(10.0, int64(7)).
					WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectCommit()
			result := &service.UsageBillingApplyResult{Applied: true}
			err = (&usageBillingRepository{}).applyUsageBillingEffects(context.Background(), tx, cmd, result)
			require.NoError(t, err)
			require.NotNil(t, result.NewBalance)
			require.InDelta(t, 90, *result.NewBalance, 0.000001)
			require.False(t, result.APIKeyQuotaExhausted)
			require.NoError(t, tx.Commit())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestApplyUsageBillingEffects_KeyCounterDatabaseErrorsStillFail(t *testing.T) {
	for _, quota := range []bool{true, false} {
		name := "rate"
		if quota {
			name = "quota"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			tx, err := db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			cmd := &service.UsageBillingCommand{APIKeyID: 7}
			if quota {
				cmd.APIKeyQuotaCost = 10
				mock.ExpectQuery(`(?s)UPDATE api_keys.*SET quota_used`).WillReturnError(sql.ErrConnDone)
			} else {
				cmd.APIKeyRateLimitCost = 10
				mock.ExpectExec(`(?s)UPDATE api_keys SET.*usage_5h`).WillReturnError(sql.ErrConnDone)
			}
			mock.ExpectRollback()
			err = (&usageBillingRepository{}).applyUsageBillingEffects(context.Background(), tx, cmd, &service.UsageBillingApplyResult{})
			require.ErrorIs(t, err, sql.ErrConnDone)
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

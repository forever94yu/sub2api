package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.UserTokenRevocationRepository = (*userRepository)(nil)

func (r *userRepository) IncrementTokenVersion(ctx context.Context, userID int64) error {
	updated, err := clientFromContext(ctx, r.client).User.Update().
		Where(user.IDEQ(userID), user.DeletedAtIsNil()).
		AddTokenVersion(1).
		Save(ctx)
	if err != nil {
		return err
	}
	if updated == 0 {
		return service.ErrUserNotFound
	}
	return nil
}

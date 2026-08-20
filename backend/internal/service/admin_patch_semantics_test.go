package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type groupPatchRepoStub struct {
	GroupRepository
	group *Group
}

func (r *groupPatchRepoStub) GetByID(context.Context, int64) (*Group, error) {
	copy := *r.group
	return &copy, nil
}

func (r *groupPatchRepoStub) Update(_ context.Context, group *Group) error {
	copy := *group
	r.group = &copy
	return nil
}

func TestUpdateGroupStatusPreservesOmittedUsageLimits(t *testing.T) {
	daily, weekly, monthly := 10.0, 20.0, 30.0
	repo := &groupPatchRepoStub{group: &Group{
		ID: 1, Platform: PlatformOpenAI, Status: StatusActive,
		DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly, MonthlyLimitUSD: &monthly,
	}}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{Status: "inactive"})
	require.NoError(t, err)
	require.Equal(t, daily, *repo.group.DailyLimitUSD)
	require.Equal(t, weekly, *repo.group.WeeklyLimitUSD)
	require.Equal(t, monthly, *repo.group.MonthlyLimitUSD)
}

func TestUpdateGroupExplicitNullClearsUsageLimit(t *testing.T) {
	daily := 10.0
	repo := &groupPatchRepoStub{group: &Group{
		ID: 1, Platform: PlatformOpenAI, Status: StatusActive, DailyLimitUSD: &daily,
	}}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{DailyLimitUSDSet: true})
	require.NoError(t, err)
	require.Nil(t, repo.group.DailyLimitUSD)
}

type proxyPatchRepoStub struct {
	ProxyRepository
	proxy *Proxy
}

func (r *proxyPatchRepoStub) GetByID(context.Context, int64) (*Proxy, error) {
	copy := *r.proxy
	return &copy, nil
}

func (r *proxyPatchRepoStub) Update(_ context.Context, proxy *Proxy) error {
	copy := *proxy
	r.proxy = &copy
	return nil
}

func TestUpdateProxyStatusPreservesOmittedOptionalSettings(t *testing.T) {
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	backupID := int64(8)
	repo := &proxyPatchRepoStub{proxy: &Proxy{
		ID: 7, Name: "proxy", Protocol: "http", Host: "old.example", Port: 8080,
		Username: "user", Password: "pass", Status: StatusActive,
		ExpiresAt: &expiresAt, FallbackMode: FallbackModeProxy, BackupProxyID: &backupID, ExpiryWarnDays: 9,
	}}
	svc := &adminServiceImpl{proxyRepo: repo}

	_, err := svc.UpdateProxy(context.Background(), 7, &UpdateProxyInput{Status: "inactive"})
	require.NoError(t, err)
	require.Equal(t, "user", repo.proxy.Username)
	require.Equal(t, "pass", repo.proxy.Password)
	require.Equal(t, expiresAt, *repo.proxy.ExpiresAt)
	require.Equal(t, FallbackModeProxy, repo.proxy.FallbackMode)
	require.Equal(t, backupID, *repo.proxy.BackupProxyID)
	require.Equal(t, 9, repo.proxy.ExpiryWarnDays)
}

func TestUpdateProxyCanExplicitlyClearCredentials(t *testing.T) {
	repo := &proxyPatchRepoStub{proxy: &Proxy{
		ID: 7, Name: "proxy", Protocol: "http", Host: "old.example", Port: 8080,
		Username: "user", Password: "pass", Status: StatusActive, FallbackMode: FallbackModeNone,
	}}
	svc := &adminServiceImpl{proxyRepo: repo}

	_, err := svc.UpdateProxy(context.Background(), 7, &UpdateProxyInput{
		Username: "", Password: "", UsernameSet: true, PasswordSet: true,
	})
	require.NoError(t, err)
	require.Empty(t, repo.proxy.Username)
	require.Empty(t, repo.proxy.Password)
}

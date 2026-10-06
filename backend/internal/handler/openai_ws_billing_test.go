package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSBillingWaitsForSettlement(t *testing.T) {
	first := openAIWSBillingSnapshot{apiKey: &service.APIKey{ID: 1}}
	state := newOpenAIWSBillingState(first)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	loaded := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- state.admit(ctx, 2, func() (openAIWSBillingSnapshot, error) {
			close(loaded)
			return openAIWSBillingSnapshot{apiKey: &service.APIKey{ID: 2}}, nil
		})
	}()
	select {
	case <-loaded:
		t.Fatal("next turn read eligibility before the first turn settled")
	case <-time.After(20 * time.Millisecond):
	}
	snapshot, finish, ok := state.beginSettlement(1)
	require.True(t, ok)
	require.Same(t, first.apiKey, snapshot.apiKey)
	finish(nil)
	require.NoError(t, <-result)
	second, _, ok := state.beginSettlement(2)
	require.True(t, ok)
	require.Equal(t, int64(2), second.apiKey.ID)
}

func TestOpenAIWSBillingSettlementFailureRejectsLaterTurns(t *testing.T) {
	state := newOpenAIWSBillingState(openAIWSBillingSnapshot{})
	_, finish, ok := state.beginSettlement(1)
	require.True(t, ok)
	failure := errors.New("billing transaction failed")
	finish(failure)
	called := false
	err := state.admit(context.Background(), 2, func() (openAIWSBillingSnapshot, error) {
		called = true
		return openAIWSBillingSnapshot{}, nil
	})
	require.ErrorIs(t, err, failure)
	require.False(t, called)
	require.False(t, shouldReportOpenAIWSProxyAccountFailure(newOpenAIWSBillingAdmissionError(err)))
}

func TestOpenAIWSBillingCancellationAndDuplicateCallbacks(t *testing.T) {
	state := newOpenAIWSBillingState(openAIWSBillingSnapshot{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := state.admit(ctx, 2, func() (openAIWSBillingSnapshot, error) {
		t.Fatal("canceled turn must not refresh eligibility")
		return openAIWSBillingSnapshot{}, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	_, finish, ok := state.beginSettlement(1)
	require.True(t, ok)
	_, _, duplicate := state.beginSettlement(1)
	require.False(t, duplicate)
	finish(nil)
	finish(errors.New("duplicate completion"))
	require.NoError(t, state.admit(context.Background(), 2, func() (openAIWSBillingSnapshot, error) {
		return openAIWSBillingSnapshot{}, nil
	}))
	_, _, stale := state.beginSettlement(1)
	require.False(t, stale)
	_, _, unadmitted := state.beginSettlement(3)
	require.False(t, unadmitted)
}

func TestOpenAIWSBillingRetryKeepsAdmittedSnapshot(t *testing.T) {
	state := newOpenAIWSBillingState(openAIWSBillingSnapshot{apiKey: &service.APIKey{ID: 1}})
	_, finish, _ := state.beginSettlement(1)
	finish(nil)
	calls := 0
	load := func() (openAIWSBillingSnapshot, error) {
		calls++
		return openAIWSBillingSnapshot{apiKey: &service.APIKey{ID: 2}}, nil
	}
	require.NoError(t, state.admit(context.Background(), 2, load))
	require.NoError(t, state.admit(context.Background(), 2, load))
	require.Equal(t, 1, calls, "same-turn upstream retry must not consume another RPM admission")
}

func TestOpenAIWSBillingRejectedAdmissionDoesNotCreateTurn(t *testing.T) {
	state := newOpenAIWSBillingState(openAIWSBillingSnapshot{})
	_, finish, _ := state.beginSettlement(1)
	finish(nil)
	err := state.admit(context.Background(), 2, func() (openAIWSBillingSnapshot, error) {
		return openAIWSBillingSnapshot{}, service.ErrInsufficientBalance
	})
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	_, _, ok := state.beginSettlement(2)
	require.False(t, ok)
}

func TestOpenAIWSBillingSettlementDetachesCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, finish := openAIWSSettlementContext(parent)
	defer finish()
	require.NoError(t, ctx.Err())
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(30*time.Second), deadline, time.Second)
}

func TestOpenAIWSBillingAdmissionCanceledDuringRefresh(t *testing.T) {
	state := newOpenAIWSBillingState(openAIWSBillingSnapshot{})
	_, finish, _ := state.beginSettlement(1)
	finish(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := state.admit(ctx, 2, func() (openAIWSBillingSnapshot, error) {
		cancel()
		return openAIWSBillingSnapshot{}, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	_, _, admitted := state.beginSettlement(2)
	require.False(t, admitted)
}

func TestOpenAIWSBillingWaitForStartedSettlementBeforeHandlerReturn(t *testing.T) {
	state := newOpenAIWSBillingState(openAIWSBillingSnapshot{})
	state.waitForSettlement()
	_, finish, _ := state.beginSettlement(1)
	drained := make(chan struct{})
	go func() {
		state.waitForSettlement()
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("handler may recycle Gin context while turn cleanup is still pending")
	case <-time.After(20 * time.Millisecond):
	}
	finish(nil)
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish after the settlement completed")
	}
}

func TestOpenAIWSBillingPricingUsesFreshGroupSnapshot(t *testing.T) {
	groupID := int64(3)
	group := &service.Group{ID: groupID, Hydrated: true, Status: service.StatusActive, Platform: service.PlatformOpenAI,
		RateMultiplier: 1, ProfitControlEnabled: true}
	connectionCtx := context.WithValue(context.Background(), ctxkey.Group, group)
	gateway := &service.OpenAIGatewayService{}
	connectionCtx, _ = gateway.WithOpenAIRequestPricingContext(connectionCtx, &groupID)
	expensiveRate := 1.2
	vetoed, _ := service.OpenAIProfitControlVeto(connectionCtx, &service.Account{ID: 2, RateMultiplier: &expensiveRate})
	require.True(t, vetoed, "the connection fixture must install an active profit gate")
	rate := 0.8
	account := &service.Account{ID: 1, RateMultiplier: &rate}
	vetoed, _ = service.OpenAIProfitControlVeto(connectionCtx, account)
	require.False(t, vetoed)
	freshGroup := *group
	freshGroup.RateMultiplier = 0.5
	snapshot := openAIWSBillingSnapshot{apiKey: &service.APIKey{GroupID: &groupID, Group: &freshGroup}}
	turnCtx, at := snapshot.pricingContext(connectionCtx, gateway)
	vetoed, _ = service.OpenAIProfitControlVeto(turnCtx, account)
	require.True(t, vetoed, "the fresh billing multiplier must also constrain profit admission")
	require.Equal(t, at, service.OpenAIPricingAtFromContext(turnCtx))
	require.Same(t, group, connectionCtx.Value(ctxkey.Group), "connection snapshot must remain immutable")
}

type openAIWSBillingHandlerKeyRepo struct {
	service.APIKeyRepository
	key     *service.APIKey
	refresh func(*service.APIKey)
}

func (r *openAIWSBillingHandlerKeyRepo) GetByKey(context.Context, string) (*service.APIKey, error) {
	key := *r.key
	if key.User != nil {
		user := *key.User
		key.User = &user
	}
	if key.Group != nil {
		group := *key.Group
		key.Group = &group
	}
	if r.refresh != nil {
		r.refresh(&key)
	}
	return &key, nil
}

func TestOpenAIResponsesWebSocketRejectsRevokedKeyBeforeSecondUpstreamTurn(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:     `{"type":"response.create","model":"gpt-5.4","stream":false}`,
				secondPayload:    `{"type":"response.create","model":"gpt-5.4","stream":false}`,
				ingressMode:      mode,
				freshAPIKey:      func(key *service.APIKey) { key.Status = service.StatusAPIKeyDisabled },
				rejectSecondTurn: true,
			})
			require.Len(t, got.upstreamPayloads, 1)
			require.Len(t, got.logs, 1)
		})
	}
}

func TestOpenAIResponsesWebSocketRejectsAmbiguousTurnType(t *testing.T) {
	for _, tt := range []struct {
		name       string
		second     string
		mode       string
		revokeKey  bool
		wantReject bool
	}{
		{
			name:   "ordinary healthy second turn",
			second: `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		},
		{
			name:       "ordinary revoked second turn",
			second:     `{"type":"response.create","model":"gpt-5.4","stream":false}`,
			revokeKey:  true,
			wantReject: true,
		},
		{
			name:       "duplicate type cannot bypass revoked key",
			second:     `{"type":"session.update","type":"response.create","model":"gpt-5.4","stream":false}`,
			revokeKey:  true,
			wantReject: true,
		},
		{
			name:       "escaped duplicate type cannot bypass revoked key",
			second:     `{"type":"session.update","\u0074ype":"response.create","model":"gpt-5.4","stream":false}`,
			revokeKey:  true,
			wantReject: true,
		},
		{
			name:       "native duplicate type rejected before admission",
			second:     `{"type":"response.create","type":"response.create","model":"gpt-5.4","stream":false}`,
			mode:       service.OpenAIWSIngressModeCtxPool,
			wantReject: true,
		},
		{
			name:       "native escaped duplicate type rejected before admission",
			second:     `{"type":"response.create","\u0074ype":"response.create","model":"gpt-5.4","stream":false}`,
			mode:       service.OpenAIWSIngressModeCtxPool,
			wantReject: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mode := tt.mode
			if mode == "" {
				mode = service.OpenAIWSIngressModePassthrough
			}
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:  `{"type":"response.create","model":"gpt-5.4","stream":false}`,
				secondPayload: tt.second,
				ingressMode:   mode,
				freshAPIKey: func(key *service.APIKey) {
					if tt.revokeKey {
						key.Status = service.StatusAPIKeyDisabled
					}
				},
				rejectSecondTurn: tt.wantReject,
			})
			wantTurns := 2
			if tt.wantReject {
				wantTurns = 1
			}
			require.Len(t, got.upstreamPayloads, wantTurns)
			require.Len(t, got.logs, wantTurns)
		})
	}
}

func TestOpenAIWSInvalidClientEnvelopeDoesNotPenalizeUpstream(t *testing.T) {
	err := service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation,
		"websocket request contains duplicate type fields", service.ErrOpenAIWSInvalidClientEnvelope)
	require.False(t, shouldReportOpenAIWSProxyAccountFailure(err))
	upstreamErr := service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation,
		"upstream authentication failed", errors.New("invalid upstream credential"))
	require.True(t, shouldReportOpenAIWSProxyAccountFailure(upstreamErr))
}

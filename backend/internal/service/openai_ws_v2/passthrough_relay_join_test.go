package openai_ws_v2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestRelayJoinWorkersWaitsForTurnSettlement(t *testing.T) {
	client := newPassthroughTestFrameConn(nil, false)
	upstream := newPassthroughTestFrameConn([]passthroughTestFrame{{
		msgType: coderws.MessageText,
		payload: []byte(`{"type":"response.completed","response":{"id":"resp_join","usage":{"input_tokens":7,"output_tokens":2}}}`),
	}}, false)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	done := make(chan RelayResult, 1)
	go func() {
		result, _ := Relay(context.Background(), client, upstream,
			[]byte(`{"type":"response.create","model":"gpt-5.4"}`), RelayOptions{
				JoinWorkersOnExit:    true,
				UpstreamDrainTimeout: 10 * time.Millisecond,
				OnTurnComplete: func(RelayTurnResult) {
					close(started)
					<-release
				},
			})
		done <- result
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("terminal callback did not start")
	}
	require.NoError(t, client.Close())
	select {
	case <-done:
		t.Fatal("relay returned while the terminal callback was settling")
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case result := <-done:
		require.EqualValues(t, 7, result.Usage.InputTokens)
		require.Equal(t, "resp_join", result.RequestID)
	case <-time.After(time.Second):
		t.Fatal("relay did not join the completed terminal callback")
	}
}

func TestRelayJoinWorkersWaitsForClientAdmission(t *testing.T) {
	client := newPassthroughTestFrameConn(nil, false)
	upstream := newPassthroughTestFrameConn(nil, false)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	done := make(chan struct{})
	go func() {
		Relay(context.Background(), client, upstream, []byte(`{"type":"response.create"}`), RelayOptions{
			JoinWorkersOnExit: true,
			ReadClientFrame: func(context.Context, FrameConn) (coderws.MessageType, []byte, error) {
				close(started)
				<-release
				return coderws.MessageText, nil, errors.New("admission rejected")
			},
		})
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("client admission did not start")
	}
	require.NoError(t, upstream.Close())
	select {
	case <-done:
		t.Fatal("relay returned while client admission was running")
	case <-time.After(250 * time.Millisecond):
	}
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not join the completed admission")
	}
}

func TestRelayJoinWorkersReleasesIdleClientRead(t *testing.T) {
	client := newPassthroughTestFrameConn(nil, false)
	upstream := newPassthroughTestFrameConn(nil, false)
	started := make(chan struct{})
	done := make(chan struct{})
	t.Cleanup(func() { _ = client.Close() })
	go func() {
		Relay(context.Background(), client, upstream, []byte(`{"type":"response.create"}`), RelayOptions{
			JoinWorkersOnExit: true,
			ReadClientFrame: func(_ context.Context, conn FrameConn) (coderws.MessageType, []byte, error) {
				close(started)
				return conn.ReadFrame(context.Background())
			},
		})
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("client read did not start")
	}
	require.NoError(t, upstream.Close())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay left the outer-context client reader blocked")
	}
}

func TestRelayJoinWorkersPreservesClientForFirstOutputFailover(t *testing.T) {
	client := &closeSpyFrameConn{}
	upstream := newPassthroughTestFrameConn([]passthroughTestFrame{{
		msgType: coderws.MessageText,
		payload: []byte(`{"type":"error"}`),
	}}, false)
	failoverErr := errors.New("retry another upstream")
	_, exit := Relay(context.Background(), client, upstream, []byte(`{"type":"response.create"}`), RelayOptions{
		JoinWorkersOnExit:               true,
		StartClientAfterFirstDownstream: true,
		BeforeWriteClient: func(coderws.MessageType, []byte, bool) error {
			return failoverErr
		},
	})
	require.NotNil(t, exit)
	require.ErrorIs(t, exit.Err, failoverErr)
	require.Zero(t, client.closeCalls.Load(), "first-output failover must retain the client connection")
}

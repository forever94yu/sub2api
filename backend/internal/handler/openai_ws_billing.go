package handler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
)

var errOpenAIWSBillingAdmission = errors.New("websocket billing admission failed")

func newOpenAIWSBillingAdmissionError(cause error) error {
	return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "billing check failed; please reconnect",
		fmt.Errorf("%w: %w", errOpenAIWSBillingAdmission, cause))
}

type openAIWSBillingSnapshot struct {
	apiKey       *service.APIKey
	subscription *service.UserSubscription
}

func (s openAIWSBillingSnapshot) pricingContext(parent context.Context, gateway *service.OpenAIGatewayService) (context.Context, time.Time) {
	// The gate and charge must see the same group snapshot. The pricing helper
	// retains any composite selection's group ID from the existing profit gate.
	ctx := context.WithValue(parent, ctxkey.Group, s.apiKey.Group)
	return gateway.WithOpenAITurnPricingContext(ctx, s.apiKey.GroupID)
}

type openAIWSBillingTurn struct {
	turn     int
	snapshot openAIWSBillingSnapshot
	done     chan struct{}
	settling bool
	err      error
}

// A downstream terminal frame can reach the client before AfterTurn finishes.
// Keep the admission barrier armed from the first turn, including passthrough
// ingress, which does not invoke BeforeTurn.
type openAIWSBillingState struct {
	admission sync.Mutex
	mu        sync.Mutex
	current   *openAIWSBillingTurn
}

func newOpenAIWSBillingState(first openAIWSBillingSnapshot) *openAIWSBillingState {
	return &openAIWSBillingState{current: &openAIWSBillingTurn{turn: 1, snapshot: first, done: make(chan struct{})}}
}

func (s *openAIWSBillingState) admit(ctx context.Context, turn int, refresh func() (openAIWSBillingSnapshot, error)) error {
	s.admission.Lock()
	defer s.admission.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	previous := s.current
	s.mu.Unlock()
	if turn == previous.turn {
		select {
		case <-previous.done:
			return previous.err
		default:
			return nil
		}
	}
	if turn != previous.turn+1 {
		return errors.New("unexpected websocket billing turn")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-previous.done:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if previous.err != nil {
		return previous.err
	}
	snapshot, err := refresh()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.current = &openAIWSBillingTurn{turn: turn, snapshot: snapshot, done: make(chan struct{})}
	s.mu.Unlock()
	return nil
}

func (s *openAIWSBillingState) snapshot(turn int) openAIWSBillingSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.turn != turn {
		return openAIWSBillingSnapshot{}
	}
	return s.current.snapshot
}

func (s *openAIWSBillingState) beginSettlement(turn int) (openAIWSBillingSnapshot, func(error), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.current
	if current.turn != turn || current.settling {
		return openAIWSBillingSnapshot{}, nil, false
	}
	current.settling = true
	var once sync.Once
	finish := func(err error) {
		once.Do(func() {
			current.err = err
			close(current.done)
		})
	}
	return current.snapshot, finish, true
}

// Passthrough only briefly drains relay goroutines on disconnect. Keep Gin's
// request context alive until a started, bounded settlement finishes cleanup.
func (s *openAIWSBillingState) waitForSettlement() {
	s.mu.Lock()
	current := s.current
	settling := current.settling
	s.mu.Unlock()
	if settling {
		<-current.done
	}
}

func openAIWSSettlementContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(usageRecordContext(parent, context.Background()), 30*time.Second)
}

package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
)

const anthropicCompatDrainTimeout = 30 * time.Second

func anthropicCompatClientGone(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Context().Err() != nil
}

// consumeAnthropicCompatStream requires the upstream terminal event before
// reporting success. A true callback result means the downstream disconnected;
// keep reading usage, bounded independently of ongoing upstream progress.
func (s *GatewayService) consumeAnthropicCompatStream(
	resp *http.Response, c *gin.Context, stream bool, consume func(*apicompat.AnthropicStreamEvent) bool,
) error {
	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	var readTimeout time.Duration
	if s.cfg != nil {
		if s.cfg.Gateway.MaxLineSize > 0 {
			maxLineSize = s.cfg.Gateway.MaxLineSize
		}
		readTimeout = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	readCtx, cancelRead := context.WithCancelCause(context.Background())
	// net/http response bodies support Close concurrently with Read. Closing the
	// body is necessary to unblock Scanner when the peer stops sending bytes.
	stopClose := context.AfterFunc(readCtx, func() { _ = resp.Body.Close() })
	drainTimeout := anthropicCompatDrainTimeout
	if readTimeout > 0 && readTimeout < drainTimeout {
		drainTimeout = readTimeout
	}
	drainTimer := time.AfterFunc(drainTimeout, func() {
		cancelRead(errors.New("upstream usage drain timed out"))
	})
	drainTimer.Stop()
	var drainOnce sync.Once
	startDrain := func() { drainOnce.Do(func() { drainTimer.Reset(drainTimeout) }) }

	var readTimer *time.Timer
	if readTimeout > 0 {
		readTimer = time.AfterFunc(readTimeout, func() {
			cancelRead(errors.New("upstream stream read timed out"))
		})
		readTimer.Stop()
	}
	requestCtx := context.Background()
	if c != nil && c.Request != nil {
		requestCtx = c.Request.Context()
	}
	stopClient := context.AfterFunc(requestCtx, func() {
		if stream {
			startDrain()
		} else {
			cancelRead(requestCtx.Err())
		}
	})
	defer func() {
		stopClient()
		// Wait for an already-running cancellation callback before stopping its
		// timer, and prevent a late callback from arming it after cleanup.
		drainOnce.Do(func() {})
		drainTimer.Stop()
		if readTimer != nil {
			readTimer.Stop()
		}
		stopClose()
		cancelRead(nil)
	}()

	process := func(frame openAICompatSSEFrame) (bool, error) {
		if strings.TrimSpace(frame.Data) == "" {
			return false, nil
		}
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(frame.Data), &event); err != nil {
			return false, newAnthropicCompatStreamFailure(resp, "Invalid upstream stream event")
		}
		if event.Type == "" {
			event.Type = frame.EventType
		}
		if event.Type == "error" {
			message := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage([]byte(frame.Data)))
			if message == "" {
				message = "Upstream stream returned an error"
			}
			return false, newAnthropicCompatStreamFailure(resp, message)
		}
		if consume(&event) {
			startDrain()
		}
		return event.Type == "message_stop", nil
	}
	var parser openAICompatSSEFrameParser
	for {
		if readTimer != nil {
			readTimer.Reset(readTimeout)
		}
		more := scanner.Scan()
		if readTimer != nil {
			readTimer.Stop()
		}
		if cause := context.Cause(readCtx); cause != nil {
			return cause
		}
		if !more {
			break
		}
		if frame, ok := parser.AddLine(scanner.Text()); ok {
			terminal, err := process(frame)
			if err != nil || terminal {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return newAnthropicCompatStreamFailure(resp, "Upstream stream read failed: "+sanitizeStreamError(err))
	}
	if frame, ok := parser.Finish(); ok {
		terminal, err := process(frame)
		if err != nil || terminal {
			return err
		}
	}
	return newAnthropicCompatStreamFailure(resp, "Upstream stream ended before message_stop")
}

func newAnthropicCompatStreamFailure(resp *http.Response, message string) *UpstreamFailoverError {
	body, _ := json.Marshal(gin.H{"error": gin.H{"type": "upstream_error", "message": message}})
	return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: body, ResponseHeaders: resp.Header}
}

func (s *GatewayService) readAnthropicCompatBufferedResponse(resp *http.Response, c *gin.Context) (*apicompat.AnthropicResponse, ClaudeUsage, error) {
	var finalResp *apicompat.AnthropicResponse
	var usage ClaudeUsage
	inputStarted := make(map[int]bool)
	err := s.consumeAnthropicCompatStream(resp, c, false, func(event *apicompat.AnthropicStreamEvent) bool {
		switch event.Type {
		case "message_start":
			if event.Message != nil {
				finalResp = event.Message
				mergeAnthropicUsage(&usage, event.Message.Usage)
			}
		case "message_delta":
			if event.Usage != nil {
				mergeAnthropicUsage(&usage, *event.Usage)
			}
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		case "content_block_start":
			if event.ContentBlock != nil && finalResp != nil {
				idx := len(finalResp.Content)
				if event.Index != nil {
					idx = *event.Index
				}
				if idx == len(finalResp.Content) {
					finalResp.Content = append(finalResp.Content, *event.ContentBlock)
				} else if idx >= 0 && idx < len(finalResp.Content) {
					finalResp.Content[idx] = *event.ContentBlock
				}
				delete(inputStarted, idx)
			}
		case "content_block_delta":
			if event.Delta == nil || finalResp == nil || event.Index == nil {
				break
			}
			idx := *event.Index
			if idx < 0 || idx >= len(finalResp.Content) {
				break
			}
			block := &finalResp.Content[idx]
			switch event.Delta.Type {
			case "text_delta":
				block.Text += event.Delta.Text
			case "thinking_delta":
				block.Thinking += event.Delta.Thinking
			case "input_json_delta":
				// content_block_start carries an initial object (normally {}).
				// The first delta starts its replacement serialized JSON value.
				if !inputStarted[idx] {
					block.Input = nil
					inputStarted[idx] = true
				}
				block.Input = appendRawJSON(block.Input, event.Delta.PartialJSON)
			}
		}
		return false
	})
	return finalResp, usage, err
}

// Before output starts the handler may fail over. After commitment, emit one
// protocol error and return a plain error so it cannot retry a second response.
func writeAnthropicCompatStreamFailure(c *gin.Context, err error, responses, disconnected bool) error {
	if !c.Writer.Written() {
		return err
	}
	message := "Upstream stream did not complete"
	var failover *UpstreamFailoverError
	if errors.As(err, &failover) {
		message = extractUpstreamErrorMessage(failover.ResponseBody)
	}
	MarkResponseCommitted(c)
	if !disconnected && !anthropicCompatClientGone(c) {
		if responses {
			payload, _ := json.Marshal(gin.H{"type": "error", "code": "upstream_error", "message": message})
			_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
		} else {
			payload, _ := json.Marshal(gin.H{"error": gin.H{"type": "upstream_error", "message": message}})
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
		}
		c.Writer.Flush()
	}
	return fmt.Errorf("upstream stream failed: %s", message)
}

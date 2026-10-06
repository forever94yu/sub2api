package service

import (
	"bytes"
	"errors"

	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

var ErrOpenAIWSInvalidClientEnvelope = errors.New("invalid websocket client envelope")

func validateOpenAIWSClientEnvelope(payload []byte) error {
	invalid := func(reason string) error {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, reason, ErrOpenAIWSInvalidClientEnvelope)
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return invalid("empty websocket request payload")
	}
	if !gjson.ValidBytes(payload) {
		return invalid("invalid websocket request payload")
	}
	root := parseRawJSONView(payload)
	if !root.IsObject() {
		return invalid("websocket request payload must be a JSON object")
	}
	seenType, duplicateType := false, false
	// Root keys are decoded by the parser, so escaped spellings of "type"
	// cannot hide a second discriminator from admission while reaching upstream.
	root.ForEach(func(key, _ gjson.Result) bool {
		if key.Str == "type" {
			if seenType {
				duplicateType = true
				return false
			}
			seenType = true
		}
		return true
	})
	if duplicateType {
		return invalid("websocket request contains duplicate type fields")
	}
	return nil
}

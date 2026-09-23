package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func validateOpenAIGPT6ChatTools(body []byte) error {
	model := gjson.GetBytes(body, "model").String()
	if !isOpenAIGPT6SolLunaModel(model) || strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String()), "none") {
		return nil
	}
	hasFunctionTools := len(gjson.GetBytes(body, "functions").Array()) > 0
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if tool.Get("type").String() == "function" {
			hasFunctionTools = true
			break
		}
	}
	if !hasFunctionTools {
		return nil
	}
	return fmt.Errorf("%s function tools require reasoning_effort=none on Chat Completions; use a Responses-capable upstream to combine tools with reasoning", normalizeKnownOpenAIGPT6Model(model))
}

// Sol and Luna accept sampling and log probabilities only with reasoning none.
// Omitted effort uses their medium default. Older model handling is unchanged.
func normalizeOpenAIGPT6SamplingBody(body []byte, model string) []byte {
	if !isOpenAIGPT6SolLunaModel(model) {
		return body
	}
	effort := gjson.GetBytes(body, "reasoning.effort").String()
	if effort == "" {
		effort = gjson.GetBytes(body, "reasoning_effort").String()
	}
	if strings.EqualFold(strings.TrimSpace(effort), "none") {
		return body
	}
	for _, field := range []string{"temperature", "top_p", "top_logprobs", "logprobs"} {
		if updated, err := sjson.DeleteBytes(body, field); err == nil {
			body = updated
		}
	}
	include := gjson.GetBytes(body, "include")
	if !include.IsArray() {
		return body
	}
	values := include.Array()
	filtered := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		if value.Type == gjson.String && value.String() == "message.output_text.logprobs" {
			continue
		}
		filtered = append(filtered, json.RawMessage(value.Raw))
	}
	if len(filtered) != len(values) {
		if updated, err := sjson.SetBytes(body, "include", filtered); err == nil {
			body = updated
		}
	}
	return body
}

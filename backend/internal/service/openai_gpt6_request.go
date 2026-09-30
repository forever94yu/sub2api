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
	if isOpenAIGPT61SolModel(model) {
		hasTools := len(gjson.GetBytes(body, "tools").Array()) > 0 || len(gjson.GetBytes(body, "functions").Array()) > 0
		for _, message := range gjson.GetBytes(body, "messages").Array() {
			role := message.Get("role").String()
			if role == "tool" || role == "function" || len(message.Get("tool_calls").Array()) > 0 || message.Get("function_call").IsObject() {
				hasTools = true
				break
			}
		}
		if hasTools {
			return fmt.Errorf("%s tool calling requires the Responses API; Chat Completions supports requests without tools", openAIGPT61SolModelID)
		}
		return nil
	}
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

// GPT-6 Sol and Luna accept sampling only with reasoning none. GPT-6.1 Sol
// does not accept sampling at any effort. Omitted effort uses medium.
func normalizeOpenAIGPT6SamplingBody(body []byte, model string) []byte {
	if !isOpenAIGPT6SolLunaModel(model) && !isOpenAIGPT61SolModel(model) {
		return body
	}
	effort := gjson.GetBytes(body, "reasoning.effort").String()
	if effort == "" {
		effort = gjson.GetBytes(body, "reasoning_effort").String()
	}
	if !isOpenAIGPT61SolModel(model) && strings.EqualFold(strings.TrimSpace(effort), "none") {
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

package service

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Sol and Luna templates fill incomplete discovery responses. A provider's
// explicit capabilities remain authoritative, including optional null values.
func fillGPT6SolLunaCodexModel(model map[string]json.RawMessage, target string) bool {
	modelID := normalizeKnownOpenAIGPT6Model(target)
	if !isOpenAIGPT6SolLunaModel(modelID) {
		return false
	}
	displayName := "GPT-6 Sol"
	if modelID == "gpt-6-luna" {
		displayName = "GPT-6 Luna"
	}
	displayJSON, _ := json.Marshal(displayName)
	changed := false
	for field, value := range map[string]string{
		"display_name":                 string(displayJSON),
		"context_window":               `1050000`,
		"max_context_window":           `1050000`,
		"input_modalities":             `["text","image"]`,
		"default_reasoning_level":      `"medium"`,
		"multi_agent_reasoning_effort": `"max"`,
		"supported_reasoning_levels": `[
			{"effort":"none","description":"No reasoning"},
			{"effort":"low","description":"Low reasoning effort"},
			{"effort":"medium","description":"Medium reasoning effort"},
			{"effort":"high","description":"High reasoning effort"},
			{"effort":"xhigh","description":"Extra high reasoning effort"},
			{"effort":"max","description":"Maximum reasoning effort"}
		]`,
	} {
		if _, exists := model[field]; exists {
			continue
		}
		model[field] = json.RawMessage(value)
		changed = true
	}
	return fillCodexModelRequiredFields(model) || changed
}

func copyValidatedGPT6SolLunaCodexFields(dst, src map[string]json.RawMessage) {
	copyValidatedAstraCodexCoreFields(dst, src)
	copyValidatedAstraCodexToolFields(dst, src)
	for _, field := range []string{"display_name", "default_reasoning_level", "multi_agent_reasoning_effort"} {
		var value string
		if json.Unmarshal(src[field], &value) == nil && strings.TrimSpace(value) != "" {
			dst[field] = src[field]
		}
	}
	// An explicit empty base prompt or provider template must reach the shared
	// fallback check intact; dropping either would synthesize a different prompt.
	var baseInstructions *string
	if raw := src["base_instructions"]; len(raw) > 0 && json.Unmarshal(raw, &baseInstructions) == nil {
		dst["base_instructions"] = raw
	}
	var modelMessages struct {
		InstructionsTemplate  *string           `json:"instructions_template"`
		InstructionsVariables map[string]string `json:"instructions_variables"`
	}
	if raw := src["model_messages"]; len(raw) > 0 && json.Unmarshal(raw, &modelMessages) == nil {
		dst["model_messages"] = raw
	}
	var levels []struct {
		Effort      string  `json:"effort"`
		Description *string `json:"description"`
	}
	if raw := bytes.TrimSpace(src["supported_reasoning_levels"]); len(raw) > 0 && raw[0] == '[' && json.Unmarshal(raw, &levels) == nil {
		valid := true
		for _, level := range levels {
			if strings.TrimSpace(level.Effort) == "" || level.Description == nil {
				valid = false
				break
			}
		}
		if valid {
			dst["supported_reasoning_levels"] = src["supported_reasoning_levels"]
		}
	}
}

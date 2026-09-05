package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Astra has five native efforts. Ultra is an optional Codex orchestration mode
// whose underlying model effort is configured separately from ordinary xhigh.
func adjustAstraCodexModel(model map[string]json.RawMessage) (bool, error) {
	changed := fillCodexModelRequiredFields(model)
	for field, value := range map[string]string{
		"display_name":                 "GPT-6-Astra",
		"multi_agent_reasoning_effort": "max",
	} {
		var current string
		if json.Unmarshal(model[field], &current) == nil && current == value {
			continue
		}
		model[field], _ = json.Marshal(value)
		changed = true
	}

	if _, exists := model["default_reasoning_level"]; !exists {
		model["default_reasoning_level"] = json.RawMessage(`"low"`)
		changed = true
	}

	var upstreamLevels []json.RawMessage
	_ = json.Unmarshal(model["supported_reasoning_levels"], &upstreamLevels)
	levelsByEffort := make(map[string]json.RawMessage, len(upstreamLevels))
	for _, level := range upstreamLevels {
		var preset struct {
			Effort string `json:"effort"`
		}
		if json.Unmarshal(level, &preset) != nil || preset.Effort == "" {
			continue
		}
		if _, exists := levelsByEffort[preset.Effort]; !exists {
			levelsByEffort[preset.Effort] = level
		}
	}
	levels := make([]json.RawMessage, 0, 6)
	for _, preset := range []struct{ effort, description string }{
		{"low", "Low reasoning effort"},
		{"medium", "Medium reasoning effort"},
		{"high", "High reasoning effort"},
		{"xhigh", "Extra high reasoning effort"},
		{"max", "Maximum reasoning effort"},
	} {
		level := levelsByEffort[preset.effort]
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(level, &fields)
		if fields == nil {
			fields = make(map[string]json.RawMessage)
			fields["effort"], _ = json.Marshal(preset.effort)
		}
		var description string
		if json.Unmarshal(fields["description"], &description) != nil || strings.TrimSpace(description) == "" {
			fields["description"], _ = json.Marshal(preset.description)
			var err error
			level, err = json.Marshal(fields)
			if err != nil {
				return false, fmt.Errorf("encode reasoning level %q: %w", preset.effort, err)
			}
		}
		levels = append(levels, level)
	}
	if ultra, exists := levelsByEffort["ultra"]; exists {
		levels = append(levels, ultra)
	}
	adjustedLevels, err := json.Marshal(levels)
	if err != nil {
		return false, fmt.Errorf("encode reasoning levels: %w", err)
	}
	var currentLevels bytes.Buffer
	if json.Compact(&currentLevels, model["supported_reasoning_levels"]) != nil || !bytes.Equal(currentLevels.Bytes(), adjustedLevels) {
		model["supported_reasoning_levels"] = adjustedLevels
		changed = true
	}
	return changed, nil
}

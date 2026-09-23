package service

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func normalizeAstraReasoningEffortBody(body []byte, model string) ([]byte, bool) {
	if !isOpenAIGPT6Model(model) {
		return body, false
	}
	changed := false
	for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
		field := gjson.GetBytes(body, path)
		if field.Type != gjson.String {
			continue
		}
		effort := strings.ToLower(strings.TrimSpace(field.String()))
		if effort != "ultra" && (effort != "minimal" || !isOpenAIGPT6SolLunaModel(model)) {
			continue
		}
		normalized := normalizeOpenAIReasoningEffortForModel(effort, model)
		if updated, err := sjson.SetBytes(body, path, normalized); err == nil {
			body = updated
			changed = true
		}
	}
	return body, changed
}

func normalizeAstraReasoningEffortForAccount(ctx context.Context, account *Account, body []byte, defaultMappedModel string) []byte {
	if account == nil || account.Platform != PlatformOpenAI {
		return body
	}
	model := resolveOpenAIForwardModel(account, gjson.GetBytes(body, "model").String(), defaultMappedModel)
	updated, changed := normalizeAstraReasoningEffortBody(body, model)
	if changed {
		// Account aliases resolve after the handler policy. Only a newly normalized
		// effort needs that policy now; reapplying it could otherwise chain maps.
		updated, _ = ApplyOpenAIReasoningEffortPolicyFromContext(ctx, updated)
	}
	return normalizeOpenAIGPT6SamplingBody(updated, model)
}

func normalizeAstraReasoningEffortForWS(body []byte, model string, hooks *OpenAIWSIngressHooks) []byte {
	updated, changed := normalizeAstraReasoningEffortBody(body, model)
	if changed && hooks != nil {
		updated, _ = ApplyOpenAIReasoningEffortPolicy(updated, hooks.MaxReasoningEffort, hooks.ReasoningEffortMappings)
	}
	return normalizeOpenAIGPT6SamplingBody(updated, model)
}

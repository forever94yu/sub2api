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
		switch effort {
		case "ultra":
		case "minimal":
			if !isOpenAIGPT6SolLunaModel(model) && !isOpenAIGPT61SolModel(model) {
				continue
			}
		case "none":
			if !isOpenAIGPT61SolModel(model) {
				continue
			}
		default:
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
	if isOpenAIGPT61SolModel(model) && ctx != nil {
		if policy, ok := ctx.Value(openAIReasoningEffortPolicyContextKey{}).(openAIReasoningEffortPolicy); ok && policy.originalEfforts != nil {
			// Resolve this model's minimum before applying the single group mapping.
			updated := restoreOpenAIReasoningEffortInput(body, policy.originalEfforts)
			updated, _ = applyOpenAIReasoningEffortPolicyForModel(updated, model, policy.maxEffort, policy.mappings)
			return normalizeOpenAIGPT6SamplingBody(updated, model)
		}
	}
	updated, changed := normalizeAstraReasoningEffortBody(body, model)
	if changed {
		// Account aliases resolve after the handler policy. Only a newly normalized
		// effort needs that policy now; reapplying it could otherwise chain maps.
		updated, _ = ApplyOpenAIReasoningEffortPolicyFromContext(ctx, updated)
	}
	if isOpenAIGPT61SolModel(model) {
		updated, _ = normalizeAstraReasoningEffortBody(updated, model)
	}
	return normalizeOpenAIGPT6SamplingBody(updated, model)
}

func normalizeAstraReasoningEffortForWS(body []byte, model string, hooks *OpenAIWSIngressHooks, originalEfforts ...openAIReasoningEffortInput) []byte {
	if isOpenAIGPT61SolModel(model) && len(originalEfforts) > 0 {
		updated := restoreOpenAIReasoningEffortInput(body, originalEfforts[0])
		maxEffort := ""
		var mappings []ReasoningEffortMapping
		if hooks != nil {
			maxEffort, mappings = hooks.MaxReasoningEffort, hooks.ReasoningEffortMappings
		}
		updated, _ = applyOpenAIReasoningEffortPolicyForModel(updated, model, maxEffort, mappings)
		return normalizeOpenAIGPT6SamplingBody(updated, model)
	}
	updated, changed := normalizeAstraReasoningEffortBody(body, model)
	if changed && hooks != nil {
		updated, _ = ApplyOpenAIReasoningEffortPolicy(updated, hooks.MaxReasoningEffort, hooks.ReasoningEffortMappings)
	}
	if isOpenAIGPT61SolModel(model) {
		updated, _ = normalizeAstraReasoningEffortBody(updated, model)
	}
	return normalizeOpenAIGPT6SamplingBody(updated, model)
}

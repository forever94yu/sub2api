package service

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func normalizeAstraReasoningEffortBody(body []byte, model string) ([]byte, bool) {
	if !strings.EqualFold(strings.TrimSpace(lastOpenAIModelSegment(model)), openAIGPT6AstraModelID) {
		return body, false
	}
	changed := false
	for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
		field := gjson.GetBytes(body, path)
		if field.Type != gjson.String || !strings.EqualFold(strings.TrimSpace(field.String()), "ultra") {
			continue
		}
		if updated, err := sjson.SetBytes(body, path, "max"); err == nil {
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
	if !changed {
		return body
	}
	// Account aliases resolve after the handler policy. Only a newly normalized
	// Ultra needs that policy now; reapplying it to other efforts could chain maps.
	updated, _ = ApplyOpenAIReasoningEffortPolicyFromContext(ctx, updated)
	return updated
}

func normalizeAstraReasoningEffortForWS(body []byte, model string, hooks *OpenAIWSIngressHooks) []byte {
	updated, changed := normalizeAstraReasoningEffortBody(body, model)
	if changed && hooks != nil {
		updated, _ = ApplyOpenAIReasoningEffortPolicy(updated, hooks.MaxReasoningEffort, hooks.ReasoningEffortMappings)
	}
	return updated
}

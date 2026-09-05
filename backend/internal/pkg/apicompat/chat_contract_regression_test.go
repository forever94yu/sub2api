package apicompat

import (
	"encoding/json"
	"strings"
	"testing"
)

func convertRegressionChat(t *testing.T, body string) *ResponsesRequest {
	t.Helper()
	var req ChatCompletionsRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	out, err := ChatCompletionsToResponses(&req)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGatewayCompatibilityRegressionDeveloperRolePreserved(t *testing.T) {
	out := convertRegressionChat(t, `{"model":"gpt-5.2","messages":[{"role":"developer","content":"Always produce JSON"},{"role":"user","content":"Hello"}]}`)
	var items []ResponsesInputItem
	if err := json.Unmarshal(out.Input, &items); err != nil {
		t.Fatal(err)
	}
	if items[0].Role != "developer" && items[0].Role != "system" {
		t.Fatalf("developer instructions downgraded to %q; input=%s", items[0].Role, out.Input)
	}
}

func TestGatewayCompatibilityRegressionNamedToolChoiceUsesResponsesShape(t *testing.T) {
	out := convertRegressionChat(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"Weather?"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{}}}}],"tool_choice":{"type":"function","function":{"name":"get_weather"}}}`)
	var choice map[string]any
	if err := json.Unmarshal(out.ToolChoice, &choice); err != nil {
		t.Fatal(err)
	}
	if choice["name"] != "get_weather" {
		t.Fatalf("Responses tool_choice lacks required top-level name; actual=%s", out.ToolChoice)
	}
}

func TestGatewayCompatibilityRegressionLegacyFunctionHistoryPreserved(t *testing.T) {
	out := convertRegressionChat(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"Weather?"},{"role":"assistant","content":null,"function_call":{"name":"get_weather","arguments":"{}"}},{"role":"function","name":"get_weather","content":"Sunny"}],"functions":[{"name":"get_weather","parameters":{"type":"object","properties":{}}}]}`)
	var items []ResponsesInputItem
	if err := json.Unmarshal(out.Input, &items); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item.Type == "function_call" {
			seen[item.CallID] = true
		}
		if item.Type == "function_call_output" && !seen[item.CallID] {
			t.Fatalf("legacy assistant.function_call lost: orphan output call_id=%q; input=%s", item.CallID, out.Input)
		}
	}
}

func TestGatewayCompatibilityRegressionResponsesRefusalPreserved(t *testing.T) {
	var resp ResponsesResponse
	if err := json.Unmarshal([]byte(`{"id":"resp_refusal","model":"gpt-4o","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot assist with that request."}]}],"usage":{"input_tokens":10,"output_tokens":8}}`), &resp); err != nil {
		t.Fatal(err)
	}
	out := ResponsesToChatCompletions(&resp, "gpt-4o")
	wire, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), "I cannot assist with that request.") {
		t.Fatalf("refusal response text lost while successful completion and usage remain; output=%s", wire)
	}
	var response struct {
		Choices []struct {
			Message struct {
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(wire, &response); err != nil {
		t.Fatal(err)
	}
	if response.Choices[0].Message.Refusal != "I cannot assist with that request." {
		t.Fatalf("refusal must use message.refusal: %s", wire)
	}
}

func TestGatewayCompatibilityRegressionResponsesRefusalStreamPreserved(t *testing.T) {
	state := NewResponsesEventToChatState()
	var event ResponsesStreamEvent
	if err := json.Unmarshal([]byte(`{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"I cannot assist with that request."}`), &event); err != nil {
		t.Fatal(err)
	}
	chunks := ResponsesEventToChatChunks(&event, state)
	if len(chunks) == 0 {
		t.Fatalf("response.refusal.delta produced no client chunks")
	}
	wire, err := json.Marshal(chunks[0])
	if err != nil {
		t.Fatal(err)
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Refusal string `json:"refusal"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(wire, &chunk); err != nil {
		t.Fatal(err)
	}
	if chunk.Choices[0].Delta.Refusal != event.Delta {
		t.Fatalf("refusal must use delta.refusal: %s", wire)
	}
}

func TestGatewayCompatibilityRegressionRepeatedLegacyCallsHaveDistinctPairedIDs(t *testing.T) {
	req := `{"model":"gpt-4o","messages":[
		{"role":"assistant","tool_calls":[{"id":"call_legacy_1","type":"function","function":{"name":"modern","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_legacy_1","content":"modern output"},
		{"role":"assistant","function_call":{"name":"lookup","arguments":"{\"key\":1}"}},
		{"role":"function","name":"lookup","content":"first"},
		{"role":"assistant","function_call":{"name":"lookup","arguments":"{\"key\":2}"}},
		{"role":"function","name":"lookup","content":"second"}]}`
	out := convertRegressionChat(t, req)
	var items []ResponsesInputItem
	if err := json.Unmarshal(out.Input, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 6 {
		t.Fatalf("all calls and outputs must survive: %s", out.Input)
	}
	seen := map[string]bool{}
	for i := 0; i < len(items); i += 2 {
		call, result := items[i], items[i+1]
		if call.Type != "function_call" || result.Type != "function_call_output" || call.CallID == "" || call.CallID != result.CallID || seen[call.CallID] {
			t.Fatalf("call IDs must be distinct and paired: %s", out.Input)
		}
		seen[call.CallID] = true
	}
	if again := convertRegressionChat(t, req); string(again.Input) != string(out.Input) {
		t.Fatal("legacy call IDs must be stable across retries")
	}
}

func TestGatewayCompatibilityRegressionBufferedRefusalWithEmptyTerminalOutput(t *testing.T) {
	accumulator := NewBufferedResponseAccumulator()
	for _, delta := range []string{"I cannot assist", " with that request."} {
		accumulator.ProcessEvent(&ResponsesStreamEvent{Type: "response.refusal.delta", Delta: delta})
	}
	resp := &ResponsesResponse{ID: "resp_refusal", Status: "completed"}
	accumulator.SupplementResponseOutput(resp)
	out := ResponsesToChatCompletions(resp, "gpt-4o")
	if got := out.Choices[0].Message.Refusal; got != "I cannot assist with that request." {
		t.Fatalf("buffered refusal lost when terminal output is empty: %q", got)
	}
}

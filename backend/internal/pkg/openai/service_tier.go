package openai

import (
	"strings"

	"github.com/tidwall/gjson"
)

// ServiceTierObserver is scoped to one upstream attempt or WebSocket turn.
// A valid terminal declaration takes precedence over earlier valid stream events.
type ServiceTierObserver struct {
	first    *string
	terminal *string
}

func (o *ServiceTierObserver) Observe(payload []byte, eventType string) {
	if o == nil {
		return
	}
	for _, path := range []string{"response.service_tier", "service_tier"} {
		value := gjson.GetBytes(payload, path)
		if value.Type != gjson.String {
			continue
		}
		tier := normalizeServiceTier(value.String(), false)
		if tier == nil || !gjson.ValidBytes(payload) {
			continue
		}
		switch strings.TrimSpace(eventType) {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			o.terminal = tier
		default:
			if gjson.GetBytes(payload, "usage").IsObject() {
				o.terminal = tier
			} else if o.first == nil {
				o.first = tier
			}
		}
		return
	}
}

func (o *ServiceTierObserver) Resolve(fallback *string) *string {
	if o != nil {
		if o.terminal != nil {
			return ResolveServiceTier(o.terminal, fallback)
		}
		if o.first != nil {
			return ResolveServiceTier(o.first, fallback)
		}
	}
	return ResolveServiceTier(nil, fallback)
}

// ResolveServiceTier prefers the actual processing tier. Missing or invalid
// upstream declarations fall back to the tier sent after local policy changes.
func ResolveServiceTier(actual, fallback *string) *string {
	if actual != nil {
		if tier := normalizeServiceTier(*actual, false); tier != nil {
			return tier
		}
	}
	if fallback != nil {
		return normalizeServiceTier(*fallback, true)
	}
	return nil
}

func normalizeServiceTier(raw string, allowAuto bool) *string {
	tier := strings.ToLower(strings.TrimSpace(raw))
	if tier == "fast" {
		tier = "priority"
	}
	switch tier {
	case "default", "priority", "flex", "scale":
		return &tier
	case "auto":
		if allowAuto {
			return &tier
		}
	}
	return nil
}

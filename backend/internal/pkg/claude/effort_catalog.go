package claude

import (
	"regexp"
	"strings"
)

var (
	effortLowMediumHigh         = []string{"low", "medium", "high"}
	effortLowMediumHighMax      = []string{"low", "medium", "high", "max"}
	effortLowMediumHighXHighMax = []string{"low", "medium", "high", "xhigh", "max"}
	effortModelDateSuffix       = regexp.MustCompile(`(?:-\d{8}|@\d{8})$`)
	effortModelVersionSuffix    = regexp.MustCompile(`-v\d+(?::\d+)?$`)
)

var effortFamilies = []struct {
	family string
	levels []string
}{
	{family: "claude-mythos-preview", levels: effortLowMediumHighMax},
	{family: "claude-mythos-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-fable-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-sonnet-4-6", levels: effortLowMediumHighMax},
	{family: "claude-sonnet-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-8", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-7", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-6", levels: effortLowMediumHighMax},
	{family: "claude-opus-4-5", levels: effortLowMediumHigh},
	{family: "claude-opus-5-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-5", levels: effortLowMediumHighXHighMax},
}

// EffortLevelsForModel returns the output_config.effort values accepted by a
// Claude model, ordered from the lightest to the deepest reasoning level.
func EffortLevelsForModel(model string) []string {
	id := normalizeEffortModelID(model)
	if id == "claude-haiku-5-5" {
		return append([]string(nil), effortLowMediumHighXHighMax...)
	}
	for _, entry := range effortFamilies {
		if id == entry.family || strings.HasPrefix(id, entry.family+"-") {
			return append([]string(nil), entry.levels...)
		}
	}
	return nil
}

// IsOpus55 identifies the fixed Opus 5.5 ID after provider/local suffix normalization.
func IsOpus55(model string) bool {
	return normalizeEffortModelID(model) == "claude-opus-5-5"
}

// IsSonnet55 matches the fixed Sonnet 5.5 version, including known provider wrappers.
func IsSonnet55(model string) bool {
	return normalizeEffortModelID(model) == "claude-sonnet-5-5"
}

// IsHaiku55 matches the fixed Haiku 5.5 version, including known provider wrappers.
func IsHaiku55(model string) bool {
	return normalizeEffortModelID(model) == "claude-haiku-5-5"
}

// HasAdaptiveThinkingDefault identifies models whose signed thinking must be
// preserved even when the request omits its thinking configuration.
func HasAdaptiveThinkingDefault(model string) bool {
	return IsOpus55(model) || IsSonnet55(model) || IsHaiku55(model)
}

func normalizeEffortModelID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndexByte(id, '/'); slash >= 0 {
		id = strings.TrimSpace(id[slash+1:])
	}
	for _, prefix := range []string{"us.anthropic.", "eu.anthropic.", "apac.anthropic.", "global.anthropic.", "anthropic."} {
		id = strings.TrimPrefix(id, prefix)
	}
	id = strings.TrimSuffix(strings.TrimSuffix(id, "-thinking"), "-latest")
	id = effortModelVersionSuffix.ReplaceAllString(id, "")
	id = effortModelDateSuffix.ReplaceAllString(id, "")
	if mapped, ok := ModelIDReverseOverrides[id]; ok {
		id = mapped
	}
	return strings.ReplaceAll(id, ".", "-")
}

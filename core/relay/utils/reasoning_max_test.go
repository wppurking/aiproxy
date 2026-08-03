package utils

import (
	"testing"

	relaymodel "github.com/labring/aiproxy/core/relay/model"
)

func TestReasoningEffortMaxConversions(t *testing.T) {
	t.Parallel()

	reasoning := relaymodel.NormalizedReasoning{
		Specified: true,
		Effort:    relaymodel.ReasoningEffortMax,
	}

	if got := ReasoningToOpenAIEffort(reasoning); got != relaymodel.ReasoningEffortMax {
		t.Fatalf("ReasoningToOpenAIEffort = %q, want %q", got, relaymodel.ReasoningEffortMax)
	}

	if got := ReasoningToBudget(reasoning); got != 32768 {
		t.Fatalf("ReasoningToBudget = %d, want 32768", got)
	}

	if got := ClaudeOutputEffort(reasoning); got != "max" {
		t.Fatalf("ClaudeOutputEffort = %q, want %q", got, "max")
	}

	if got := doubaoReasoningEffort(reasoning); got != "high" {
		t.Fatalf("doubaoReasoningEffort = %q, want %q", got, "high")
	}
}

func TestGeminiThinkingLevelForModelMax(t *testing.T) {
	t.Parallel()

	reasoning := relaymodel.NormalizedReasoning{
		Specified: true,
		Effort:    relaymodel.ReasoningEffortMax,
	}

	if got := GeminiThinkingLevelForModel("gemini-3-pro", reasoning); got != "high" {
		t.Fatalf("Gemini 3 Pro thinkingLevel = %q, want %q", got, "high")
	}

	if got := GeminiThinkingLevelForModel("gemini-3-flash", reasoning); got != "high" {
		t.Fatalf("Gemini 3 Flash thinkingLevel = %q, want %q", got, "high")
	}
}

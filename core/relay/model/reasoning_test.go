package model

import "testing"

func TestNormalizeReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  ReasoningEffort
	}{
		{
			name:  "none",
			input: "none",
			want:  ReasoningEffortNone,
		},
		{
			name:  "off alias",
			input: "off",
			want:  ReasoningEffortNone,
		},
		{
			name:  "disabled alias",
			input: "disabled",
			want:  ReasoningEffortNone,
		},
		{
			name:  "minimal",
			input: "minimal",
			want:  ReasoningEffortMinimal,
		},
		{
			name:  "low",
			input: "low",
			want:  ReasoningEffortLow,
		},
		{
			name:  "medium",
			input: "medium",
			want:  ReasoningEffortMedium,
		},
		{
			name:  "med alias",
			input: "med",
			want:  ReasoningEffortMedium,
		},
		{
			name:  "high",
			input: "high",
			want:  ReasoningEffortHigh,
		},
		{
			name:  "xhigh",
			input: "xhigh",
			want:  ReasoningEffortXHigh,
		},
		{
			name:  "max",
			input: "max",
			want:  ReasoningEffortMax,
		},
		{
			name:  "maximum alias",
			input: "maximum",
			want:  ReasoningEffortMax,
		},
		{
			name:  "case and whitespace insensitive",
			input: "  MAX ",
			want:  ReasoningEffortMax,
		},
		{
			name:  "unknown",
			input: "ultra",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := NormalizeReasoningEffort(tt.input)
			if got != tt.want {
				t.Fatalf("NormalizeReasoningEffort(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

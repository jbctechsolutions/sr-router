package router

import (
	"strings"
	"testing"
)

func TestInjectSuffix(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	// minimax-m2 has a prompt suffix defined in models.yaml.
	system := "You are a helpful assistant."
	result := r.InjectSuffix("minimax-m2", system)
	if result == system {
		t.Error("expected suffix to be injected for minimax-m2")
	}
}

func TestNoSuffixForClaude(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	system := "You are a helpful assistant."
	result := r.InjectSuffix("claude-sonnet", system)
	if result != system {
		t.Errorf("expected no suffix injection for claude-sonnet, got %q", result)
	}
}

func TestInjectSuffix_Table(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	const miniMaxSuffixSnippet = "CRITICAL FORMATTING RULES"

	tests := []struct {
		name         string
		modelName    string
		systemPrompt string
		wantSame     bool   // true when result must equal systemPrompt
		wantContains string // non-empty: result must contain this substring
	}{
		{
			name:         "known model with suffix, non-empty system prompt",
			modelName:    "minimax-m2",
			systemPrompt: "You are a helpful assistant.",
			wantContains: miniMaxSuffixSnippet,
		},
		{
			name:         "known model with suffix, empty system prompt",
			modelName:    "minimax-m2",
			systemPrompt: "",
			wantContains: miniMaxSuffixSnippet,
		},
		{
			name:         "claude-sonnet has null suffix",
			modelName:    "claude-sonnet",
			systemPrompt: "You are a helpful assistant.",
			wantSame:     true,
		},
		{
			name:         "claude-opus has null suffix",
			modelName:    "claude-opus",
			systemPrompt: "You are a helpful assistant.",
			wantSame:     true,
		},
		{
			name:         "unknown model returns systemPrompt unchanged",
			modelName:    "does-not-exist",
			systemPrompt: "Stay on topic.",
			wantSame:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.InjectSuffix(tt.modelName, tt.systemPrompt)

			if tt.wantSame && result != tt.systemPrompt {
				t.Errorf("InjectSuffix(%q, %q) = %q, want unchanged system prompt",
					tt.modelName, tt.systemPrompt, result)
			}

			if tt.wantContains != "" && !strings.Contains(result, tt.wantContains) {
				t.Errorf("InjectSuffix(%q, %q) = %q, want it to contain %q",
					tt.modelName, tt.systemPrompt, result, tt.wantContains)
			}
		})
	}
}

func TestInjectSuffix_Separator(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	// When both system prompt and suffix are non-empty they must be joined
	// with exactly "\n\n".
	system := "You are a helpful assistant."
	result := r.InjectSuffix("minimax-m2", system)

	if !strings.HasPrefix(result, system+"\n\n") {
		t.Errorf("expected result to start with system prompt followed by \\n\\n, got: %q", result)
	}
}

func TestInjectSuffix_EmptySystemPrompt(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	// When systemPrompt is empty, result must equal the trimmed suffix (no
	// leading blank lines).
	result := r.InjectSuffix("minimax-m2", "")
	if strings.HasPrefix(result, "\n") {
		t.Errorf("result should not start with newline when systemPrompt is empty, got: %q", result)
	}
	if result == "" {
		t.Error("expected non-empty result for minimax-m2 with empty system prompt")
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binary holds the path to the compiled sr-router binary used by every test.
var binary string

// TestMain builds the binary once before any test runs and removes it afterwards.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "sr-router-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	binary = filepath.Join(tmp, "sr-router")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build binary: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// configDir returns the absolute path to the config directory that lives next
// to the cmd/ package.
func configDir(t *testing.T) string {
	t.Helper()
	// cmd/main_test.go is inside cmd/; config/ is a sibling directory.
	dir, err := filepath.Abs(filepath.Join("..", "config"))
	if err != nil {
		t.Fatalf("resolving config dir: %v", err)
	}
	return dir
}

// run executes the binary with the given arguments and the --config flag
// pointing at the real config directory. It returns stdout, stderr, and any
// error.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	full := append([]string{"--config", configDir(t)}, args...)
	cmd := exec.Command(binary, full...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// --------------------------------------------------------------------------
// route command
// --------------------------------------------------------------------------

func TestRouteCommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantInOut  []string // all must appear in stdout
		wantNotErr bool     // expect exit code 0
	}{
		{
			name:       "code prompt classifies and routes",
			args:       []string{"route", "Write a Go function for rate limiting"},
			wantInOut:  []string{"Route Class:", "Task Type:", "Model:", "Tier:"},
			wantNotErr: true,
		},
		{
			name:       "summarization prompt routes",
			args:       []string{"route", "Summarize this document about climate change"},
			wantInOut:  []string{"Route Class:", "Task Type:", "Model:"},
			wantNotErr: true,
		},
		{
			name:       "architecture prompt routes",
			args:       []string{"route", "Design a microservice architecture for payments"},
			wantInOut:  []string{"Route Class:", "Task Type:", "Model:"},
			wantNotErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := run(t, tt.args...)
			if tt.wantNotErr && err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
			}
			for _, want := range tt.wantInOut {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q\ngot: %s", want, stdout)
				}
			}
		})
	}
}

func TestRouteCodePromptClassifiesAsCode(t *testing.T) {
	stdout, stderr, err := run(t, "route", "--json", "Write a Go function for sorting")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	var out struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON: %v\nstdout: %s", err, stdout)
	}
	if out.Task != "code" {
		t.Errorf("expected task %q, got %q", "code", out.Task)
	}
}

// --------------------------------------------------------------------------
// route --json flag
// --------------------------------------------------------------------------

func TestRouteJSONOutput(t *testing.T) {
	stdout, stderr, err := run(t, "route", "--json", "Write a Go function for rate limiting")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	var out map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nstdout: %s", err, stdout)
	}

	requiredKeys := []string{"model", "tier", "task", "route_class", "score"}
	for _, key := range requiredKeys {
		if _, ok := out[key]; !ok {
			t.Errorf("JSON output missing key %q\ngot: %s", key, stdout)
		}
	}

	// score should be a positive number
	score, ok := out["score"].(float64)
	if !ok || score <= 0 {
		t.Errorf("expected positive score, got %v", out["score"])
	}

	// model and tier should be non-empty strings
	for _, key := range []string{"model", "tier", "task", "route_class"} {
		val, ok := out[key].(string)
		if !ok || val == "" {
			t.Errorf("expected non-empty string for %q, got %v", key, out[key])
		}
	}
}

func TestRouteJSONOutputForDifferentPrompts(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		wantTask string
	}{
		{
			name:     "code task",
			prompt:   "Write a Go function for rate limiting",
			wantTask: "code",
		},
		{
			name:     "summarization task",
			prompt:   "Summarize this document",
			wantTask: "summarization",
		},
		{
			name:     "architecture task",
			prompt:   "Design a microservice architecture",
			wantTask: "architecture",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := run(t, "route", "--json", tt.prompt)
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
			}

			var out struct {
				Task string `json:"task"`
			}
			if err := json.Unmarshal([]byte(stdout), &out); err != nil {
				t.Fatalf("failed to parse JSON: %v\nstdout: %s", err, stdout)
			}
			if out.Task != tt.wantTask {
				t.Errorf("expected task %q, got %q", tt.wantTask, out.Task)
			}
		})
	}
}

// --------------------------------------------------------------------------
// classify command
// --------------------------------------------------------------------------

func TestClassifyCommand(t *testing.T) {
	tests := []struct {
		name           string
		prompt         string
		wantRouteClass string
		wantTaskType   string
	}{
		{
			name:           "code prompt",
			prompt:         "Write a Go function for rate limiting",
			wantRouteClass: "interactive",
			wantTaskType:   "code",
		},
		{
			name:           "architecture prompt",
			prompt:         "Design a microservice architecture for payments",
			wantRouteClass: "interactive",
			wantTaskType:   "architecture",
		},
		{
			name:           "summarization prompt",
			prompt:         "Summarize this document for me",
			wantRouteClass: "interactive",
			wantTaskType:   "summarization",
		},
		{
			name:           "compaction route class via content pattern",
			prompt:         "Please summarize this conversation history",
			wantRouteClass: "compaction",
			wantTaskType:   "summarization",
		},
		{
			name:           "chat prompt",
			prompt:         "What is a goroutine?",
			wantRouteClass: "interactive",
			wantTaskType:   "chat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := run(t, "classify", tt.prompt)
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
			}

			if !strings.Contains(stdout, "Route Class:") {
				t.Error("output missing Route Class line")
			}
			if !strings.Contains(stdout, "Task Type:") {
				t.Error("output missing Task Type line")
			}

			// Verify specific classification values.
			if !strings.Contains(stdout, tt.wantRouteClass) {
				t.Errorf("expected route class %q in output\ngot: %s", tt.wantRouteClass, stdout)
			}
			if !strings.Contains(stdout, tt.wantTaskType) {
				t.Errorf("expected task type %q in output\ngot: %s", tt.wantTaskType, stdout)
			}
		})
	}
}

func TestClassifyOutputFields(t *testing.T) {
	stdout, stderr, err := run(t, "classify", "Write a function to handle errors")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	expectedFields := []string{
		"Route Class:",
		"Task Type:",
		"Tier:",
		"Min Quality:",
		"Latency Budget:",
		"Confidence:",
	}

	for _, field := range expectedFields {
		if !strings.Contains(stdout, field) {
			t.Errorf("output missing field %q\ngot: %s", field, stdout)
		}
	}
}

// --------------------------------------------------------------------------
// models command
// --------------------------------------------------------------------------

func TestModelsCommand(t *testing.T) {
	stdout, stderr, err := run(t, "models")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	// Header should be present.
	if !strings.Contains(stdout, "NAME") {
		t.Error("output missing NAME header")
	}
	if !strings.Contains(stdout, "PROVIDER") {
		t.Error("output missing PROVIDER header")
	}

	// Known models from models.yaml should appear.
	expectedModels := []string{
		"claude-opus",
		"claude-sonnet",
		"minimax-m2",
		"cerebras-glm",
		"ollama/llama3.2",
		"ollama/codellama",
	}
	for _, model := range expectedModels {
		if !strings.Contains(stdout, model) {
			t.Errorf("output missing model %q\ngot: %s", model, stdout)
		}
	}
}

func TestModelsCommandListsProviders(t *testing.T) {
	stdout, stderr, err := run(t, "models")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	expectedProviders := []string{"anthropic", "openai_compat", "ollama"}
	for _, provider := range expectedProviders {
		if !strings.Contains(stdout, provider) {
			t.Errorf("output missing provider %q\ngot: %s", provider, stdout)
		}
	}
}

// --------------------------------------------------------------------------
// models --tier flag
// --------------------------------------------------------------------------

func TestModelsTierFilter(t *testing.T) {
	tests := []struct {
		tier           string
		wantModels     []string
		dontWantModels []string
	}{
		{
			tier:           "premium",
			wantModels:     []string{"claude-opus", "claude-sonnet"},
			dontWantModels: []string{"minimax-m2", "cerebras-glm"},
		},
		{
			tier:           "budget",
			wantModels:     []string{"minimax-m2", "ollama/llama3.2"},
			dontWantModels: []string{"claude-opus"},
		},
		{
			tier:           "speed",
			wantModels:     []string{"cerebras-glm", "ollama/llama3.2"},
			dontWantModels: []string{"claude-opus", "minimax-m2"},
		},
		{
			tier:           "free",
			wantModels:     []string{"ollama/llama3.2", "ollama/codellama"},
			dontWantModels: []string{"claude-opus", "claude-sonnet"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			stdout, stderr, err := run(t, "models", "--tier", tt.tier)
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
			}

			for _, model := range tt.wantModels {
				if !strings.Contains(stdout, model) {
					t.Errorf("tier %q: output missing expected model %q\ngot: %s", tt.tier, model, stdout)
				}
			}

			// Lines in the output table should only contain models from the
			// specified tier. We check that models belonging to other tiers do
			// NOT appear as row entries. We skip the header line by looking for
			// lines that start with the model name pattern.
			lines := strings.Split(stdout, "\n")
			for _, model := range tt.dontWantModels {
				for _, line := range lines {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, model) {
						t.Errorf("tier %q: output should not list model %q\nline: %s", tt.tier, model, line)
					}
				}
			}
		})
	}
}

func TestModelsTierFilterUnknownTier(t *testing.T) {
	_, _, err := run(t, "models", "--tier", "nonexistent")
	if err == nil {
		t.Error("expected error for unknown tier, got nil")
	}
}

// --------------------------------------------------------------------------
// config validate command
// --------------------------------------------------------------------------

func TestConfigValidate(t *testing.T) {
	stdout, stderr, err := run(t, "config", "validate")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Config is valid!") {
		t.Errorf("expected 'Config is valid!' in output\ngot: %s", stdout)
	}
}

func TestConfigInit(t *testing.T) {
	stdout, stderr, err := run(t, "config", "init")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Config directory:") {
		t.Errorf("expected 'Config directory:' in output\ngot: %s", stdout)
	}
}

// --------------------------------------------------------------------------
// Error cases
// --------------------------------------------------------------------------

func TestRouteEmptyPromptError(t *testing.T) {
	// The route command with no arguments should fail.
	_, _, err := run(t, "route")
	if err == nil {
		t.Error("expected error for route with no arguments, got nil")
	}
}

func TestClassifyNoArgsError(t *testing.T) {
	// The classify command requires at least 1 argument.
	_, _, err := run(t, "classify")
	if err == nil {
		t.Error("expected error for classify with no arguments, got nil")
	}
}

func TestMissingConfigDir(t *testing.T) {
	// Point to a nonexistent config directory. We bypass the run() helper
	// to use a custom --config path.
	cmd := exec.Command(binary, "--config", "/nonexistent/config/path", "route", "hello")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		t.Error("expected error for missing config directory, got nil")
	}
}

func TestMissingConfigDirClassify(t *testing.T) {
	cmd := exec.Command(binary, "--config", "/nonexistent/config/path", "classify", "hello")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		t.Error("expected error for missing config directory on classify, got nil")
	}
}

func TestMissingConfigDirModels(t *testing.T) {
	cmd := exec.Command(binary, "--config", "/nonexistent/config/path", "models")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		t.Error("expected error for missing config directory on models, got nil")
	}
}

func TestMissingConfigDirValidate(t *testing.T) {
	cmd := exec.Command(binary, "--config", "/nonexistent/config/path", "config", "validate")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		t.Error("expected error for missing config directory on validate, got nil")
	}
}

// --------------------------------------------------------------------------
// route with --background and --interactive flags
// --------------------------------------------------------------------------

func TestRouteBackgroundFlag(t *testing.T) {
	stdout, stderr, err := run(t, "route", "--background", "Process this batch job")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Route Class:") {
		t.Errorf("output missing Route Class\ngot: %s", stdout)
	}
}

func TestRouteInteractiveFlag(t *testing.T) {
	stdout, stderr, err := run(t, "route", "--interactive", "Help me with this code")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Route Class:") {
		t.Errorf("output missing Route Class\ngot: %s", stdout)
	}
}

// --------------------------------------------------------------------------
// route --json structure with --background flag
// --------------------------------------------------------------------------

func TestRouteJSONWithBackgroundFlag(t *testing.T) {
	stdout, stderr, err := run(t, "route", "--json", "--background", "Process this batch")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	var out struct {
		Model      string  `json:"model"`
		Tier       string  `json:"tier"`
		Task       string  `json:"task"`
		RouteClass string  `json:"route_class"`
		Score      float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON: %v\nstdout: %s", err, stdout)
	}

	if out.RouteClass != "background" {
		t.Errorf("expected route_class %q, got %q", "background", out.RouteClass)
	}
}

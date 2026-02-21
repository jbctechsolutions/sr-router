package router

import (
	"testing"

	"github.com/jbctechsolutions/sr-router/config"
)

func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("../config")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	return cfg
}

func TestClassifyTaskType(t *testing.T) {
	cfg := loadTestConfig(t)
	c := NewClassifier(cfg)

	tests := []struct {
		prompt   string
		wantType string
	}{
		{"Write a Go function for rate limiting", "code"},
		{"Design a microservice architecture", "architecture"},
		{"Summarize this document", "summarization"},
		{"Extract emails from this CSV", "data_extraction"},
		{"Translate this to Spanish", "translation"},
		{"What is a goroutine?", "chat"},
	}

	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			result := c.Classify(tt.prompt, nil)
			if result.TaskType != tt.wantType {
				t.Errorf("got task type %q, want %q", result.TaskType, tt.wantType)
			}
		})
	}
}

func TestClassifyRouteClass(t *testing.T) {
	cfg := loadTestConfig(t)
	c := NewClassifier(cfg)

	result := c.Classify("Please summarize this conversation history", nil)
	if result.RouteClass != "compaction" {
		t.Errorf("expected compaction route class, got %s", result.RouteClass)
	}
}

func TestClassifyRouteClassFromHeaders(t *testing.T) {
	cfg := loadTestConfig(t)
	c := NewClassifier(cfg)

	headers := map[string]string{"x-request-type": "background"}
	result := c.Classify("Do something", headers)
	if result.RouteClass != "background" {
		t.Errorf("expected background route class, got %s", result.RouteClass)
	}
}

func TestClassifySetsTierFromRouteClass(t *testing.T) {
	cfg := loadTestConfig(t)
	c := NewClassifier(cfg)

	headers := map[string]string{"x-request-type": "background"}
	result := c.Classify("Process this batch", headers)
	if result.Tier != "budget" {
		t.Errorf("expected budget tier for background, got %s", result.Tier)
	}
}

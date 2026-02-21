package telemetry

import (
	"os"
	"testing"
)

func TestRecordAndQueryEvents(t *testing.T) {
	dbPath := "test_telemetry.db"
	defer os.Remove(dbPath)

	c, err := NewCollector(dbPath)
	if err != nil {
		t.Fatalf("failed to create collector: %v", err)
	}
	defer c.Close()

	err = c.RecordRouting(RoutingEvent{
		ID:            "test-1",
		RouteClass:    "interactive",
		TaskType:      "code",
		Tier:          "premium",
		SelectedModel: "claude-sonnet",
		LatencyMs:     1500,
		EstimatedCost: 0.015,
	})
	if err != nil {
		t.Fatalf("failed to record event: %v", err)
	}

	stats, err := c.GetStats("")
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", stats.TotalRequests)
	}
}

func TestRecordFailover(t *testing.T) {
	dbPath := "test_failover.db"
	defer os.Remove(dbPath)

	c, err := NewCollector(dbPath)
	if err != nil {
		t.Fatalf("failed to create collector: %v", err)
	}
	defer c.Close()

	c.RecordRouting(RoutingEvent{
		ID:            "fo-1",
		RouteClass:    "interactive",
		TaskType:      "code",
		Tier:          "premium",
		SelectedModel: "claude-opus",
	})

	err = c.RecordFailover("fo-1", "claude-opus", "claude-sonnet")
	if err != nil {
		t.Fatalf("failed to record failover: %v", err)
	}
}

package telemetry

import (
	"database/sql"
	"encoding/json"

	_ "github.com/mattn/go-sqlite3"
)

// Collector records routing events and exposes aggregate stats via SQLite.
type Collector struct {
	db *sql.DB
}

// RoutingEvent captures a single model-selection decision.
type RoutingEvent struct {
	ID            string
	RouteClass    string
	TaskType      string
	Tier          string
	SelectedModel string
	Alternatives  []string
	LatencyMs     int
	EstimatedCost float64
	FailoverFrom  string
	UserRating    int
	UserOverride  string
}

// Stats holds aggregate routing telemetry.
type Stats struct {
	TotalRequests int
	TotalCost     float64
	ByModel       map[string]int
	ByTier        map[string]int
	FailoverCount int
}

// NewCollector opens (or creates) the SQLite database at dbPath and ensures
// the routing_events table exists.
func NewCollector(dbPath string) (*Collector, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS routing_events (
		id TEXT PRIMARY KEY,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		route_class TEXT,
		task_type TEXT,
		tier TEXT,
		selected_model TEXT,
		alternatives TEXT,
		latency_ms INTEGER,
		estimated_cost REAL,
		failover_from TEXT,
		user_rating INTEGER,
		user_override TEXT
	)`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Collector{db: db}, nil
}

// Close releases the database connection.
func (c *Collector) Close() error {
	return c.db.Close()
}

// RecordRouting inserts a new routing event.
func (c *Collector) RecordRouting(e RoutingEvent) error {
	altsJSON, _ := json.Marshal(e.Alternatives)
	_, err := c.db.Exec(
		`INSERT INTO routing_events
			(id, route_class, task_type, tier, selected_model, alternatives, latency_ms, estimated_cost)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.RouteClass, e.TaskType, e.Tier, e.SelectedModel,
		string(altsJSON), e.LatencyMs, e.EstimatedCost,
	)
	return err
}

// RecordFailover updates an existing event to reflect the model that was
// actually used after a failover.
func (c *Collector) RecordFailover(eventID, fromModel, toModel string) error {
	_, err := c.db.Exec(
		`UPDATE routing_events SET failover_from = ?, selected_model = ? WHERE id = ?`,
		fromModel, toModel, eventID,
	)
	return err
}

// RecordFeedback stores user-provided rating and optional override for an event.
func (c *Collector) RecordFeedback(eventID string, rating int, override string) error {
	_, err := c.db.Exec(
		`UPDATE routing_events SET user_rating = ?, user_override = ? WHERE id = ?`,
		rating, override, eventID,
	)
	return err
}

// GetStats returns aggregate stats. When modelFilter is non-empty, TotalRequests
// and TotalCost are scoped to that model only; ByModel, ByTier, and FailoverCount
// always cover all events.
func (c *Collector) GetStats(modelFilter string) (*Stats, error) {
	stats := &Stats{
		ByModel: make(map[string]int),
		ByTier:  make(map[string]int),
	}

	// Total requests and cost, optionally filtered by model.
	query := `SELECT COUNT(*), COALESCE(SUM(estimated_cost), 0) FROM routing_events`
	args := []interface{}{}
	if modelFilter != "" {
		query += ` WHERE selected_model = ?`
		args = append(args, modelFilter)
	}

	if err := c.db.QueryRow(query, args...).Scan(&stats.TotalRequests, &stats.TotalCost); err != nil {
		return nil, err
	}

	// Breakdown by model.
	rows, err := c.db.Query(
		`SELECT selected_model, COUNT(*) FROM routing_events GROUP BY selected_model`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var count int
		if err := rows.Scan(&model, &count); err != nil {
			return nil, err
		}
		stats.ByModel[model] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Breakdown by tier.
	rows2, err := c.db.Query(
		`SELECT tier, COUNT(*) FROM routing_events GROUP BY tier`,
	)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var tier string
		var count int
		if err := rows2.Scan(&tier, &count); err != nil {
			return nil, err
		}
		stats.ByTier[tier] = count
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// Failover count across all events.
	if err := c.db.QueryRow(
		`SELECT COUNT(*) FROM routing_events WHERE failover_from IS NOT NULL`,
	).Scan(&stats.FailoverCount); err != nil {
		return nil, err
	}

	return stats, nil
}

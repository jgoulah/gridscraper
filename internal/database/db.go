package database

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jgoulah/gridscraper/pkg/models"
	_ "modernc.org/sqlite"
)

// DB wraps the database connection
type DB struct {
	conn *sql.DB
}

// New creates a new database connection and initializes the schema
func New(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// initSchema creates the necessary tables
func (db *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS usage_data (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL,
		start_time TEXT,
		end_time TEXT,
		kwh REAL NOT NULL,
		service TEXT NOT NULL,
		created_at TEXT NOT NULL,
		published INTEGER DEFAULT 0,
		UNIQUE(start_time, service)
	);
	CREATE INDEX IF NOT EXISTS idx_usage_date ON usage_data(date);
	CREATE INDEX IF NOT EXISTS idx_usage_service ON usage_data(service);
	CREATE INDEX IF NOT EXISTS idx_usage_start_time ON usage_data(start_time);
	CREATE INDEX IF NOT EXISTS idx_usage_published ON usage_data(published);
	`

	_, err := db.conn.Exec(schema)
	if err != nil {
		return err
	}

	// Add columns to existing tables (migration)
	// These will fail silently if columns already exist
	db.conn.Exec(`ALTER TABLE usage_data ADD COLUMN start_time TEXT`)
	db.conn.Exec(`ALTER TABLE usage_data ADD COLUMN end_time TEXT`)
	db.conn.Exec(`ALTER TABLE usage_data ADD COLUMN published INTEGER DEFAULT 0`)

	return nil
}

// InsertUsage inserts a usage record, ignoring duplicates
func (db *DB) InsertUsage(data *models.UsageData) error {
	query := `
	INSERT OR IGNORE INTO usage_data (date, start_time, end_time, kwh, service, created_at)
	VALUES (?, ?, ?, ?, ?, ?)
	`

	dateStr := data.Date.Format("2006-01-02")
	var startTimeStr, endTimeStr string
	if !data.StartTime.IsZero() {
		startTimeStr = data.StartTime.Format("2006-01-02 15:04:05")
	}
	if !data.EndTime.IsZero() {
		endTimeStr = data.EndTime.Format("2006-01-02 15:04:05")
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)

	_, err := db.conn.Exec(query, dateStr, startTimeStr, endTimeStr, data.KWh, data.Service, createdAt)
	if err != nil {
		return fmt.Errorf("inserting usage data: %w", err)
	}

	return nil
}

// GetUsage retrieves usage data for a specific date and service
func (db *DB) GetUsage(date time.Time, service string) (*models.UsageData, error) {
	query := `
	SELECT id, date, start_time, end_time, kwh, service
	FROM usage_data
	WHERE date = ? AND service = ?
	`

	dateStr := date.Format("2006-01-02")
	row := db.conn.QueryRow(query, dateStr, service)

	var data models.UsageData
	var dateStrResult string
	var startTimeStr, endTimeStr sql.NullString

	err := row.Scan(&data.ID, &dateStrResult, &startTimeStr, &endTimeStr, &data.KWh, &data.Service)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying usage data: %w", err)
	}

	data.Date, err = time.Parse("2006-01-02", dateStrResult)
	if err != nil {
		return nil, fmt.Errorf("parsing date: %w", err)
	}

	if startTimeStr.Valid && startTimeStr.String != "" {
		data.StartTime, err = time.Parse("2006-01-02 15:04:05", startTimeStr.String)
		if err != nil {
			return nil, fmt.Errorf("parsing start_time: %w", err)
		}
	}

	if endTimeStr.Valid && endTimeStr.String != "" {
		data.EndTime, err = time.Parse("2006-01-02 15:04:05", endTimeStr.String)
		if err != nil {
			return nil, fmt.Errorf("parsing end_time: %w", err)
		}
	}

	return &data, nil
}

// ListUsage retrieves all usage data for a service, ordered by date
func (db *DB) ListUsage(service string) ([]models.UsageData, error) {
	query := `
	SELECT id, date, start_time, end_time, kwh, service
	FROM usage_data
	WHERE service = ?
	ORDER BY date DESC
	`

	rows, err := db.conn.Query(query, service)
	if err != nil {
		return nil, fmt.Errorf("querying usage data: %w", err)
	}
	defer rows.Close()

	var results []models.UsageData
	for rows.Next() {
		var data models.UsageData
		var dateStr string
		var startTimeStr, endTimeStr sql.NullString

		if err := rows.Scan(&data.ID, &dateStr, &startTimeStr, &endTimeStr, &data.KWh, &data.Service); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		data.Date, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("parsing date: %w", err)
		}

		if startTimeStr.Valid && startTimeStr.String != "" {
			data.StartTime, err = time.Parse("2006-01-02 15:04:05", startTimeStr.String)
			if err != nil {
				return nil, fmt.Errorf("parsing start_time: %w", err)
			}
		}

		if endTimeStr.Valid && endTimeStr.String != "" {
			data.EndTime, err = time.Parse("2006-01-02 15:04:05", endTimeStr.String)
			if err != nil {
				return nil, fmt.Errorf("parsing end_time: %w", err)
			}
		}

		results = append(results, data)
	}

	return results, rows.Err()
}

// HasData checks if data exists for a given date and service
func (db *DB) HasData(date time.Time, service string) (bool, error) {
	data, err := db.GetUsage(date, service)
	if err != nil {
		return false, err
	}
	return data != nil, nil
}

// ListUnpublishedUsage retrieves all unpublished usage data for a service, ordered by date
func (db *DB) ListUnpublishedUsage(service string) ([]models.UsageData, error) {
	query := `
	SELECT id, date, start_time, end_time, kwh, service
	FROM usage_data
	WHERE service = ? AND published = 0
	ORDER BY date DESC
	`

	rows, err := db.conn.Query(query, service)
	if err != nil {
		return nil, fmt.Errorf("querying unpublished usage data: %w", err)
	}
	defer rows.Close()

	var results []models.UsageData
	for rows.Next() {
		var data models.UsageData
		var dateStr string
		var startTimeStr, endTimeStr sql.NullString

		if err := rows.Scan(&data.ID, &dateStr, &startTimeStr, &endTimeStr, &data.KWh, &data.Service); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		data.Date, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("parsing date: %w", err)
		}

		if startTimeStr.Valid && startTimeStr.String != "" {
			data.StartTime, err = time.Parse("2006-01-02 15:04:05", startTimeStr.String)
			if err != nil {
				return nil, fmt.Errorf("parsing start_time: %w", err)
			}
		}

		if endTimeStr.Valid && endTimeStr.String != "" {
			data.EndTime, err = time.Parse("2006-01-02 15:04:05", endTimeStr.String)
			if err != nil {
				return nil, fmt.Errorf("parsing end_time: %w", err)
			}
		}

		results = append(results, data)
	}

	return results, rows.Err()
}

// MarkPublished marks a usage record as published
func (db *DB) MarkPublished(id int) error {
	query := `UPDATE usage_data SET published = 1 WHERE id = ?`
	_, err := db.conn.Exec(query, id)
	if err != nil {
		return fmt.Errorf("marking record as published: %w", err)
	}
	return nil
}

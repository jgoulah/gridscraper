package models

import "time"

// UsageData represents a single day's electricity usage
type UsageData struct {
	ID        int       `json:"id"`
	Date      time.Time `json:"date"`       // Just the date (for querying)
	StartTime time.Time `json:"start_time"` // Full timestamp
	EndTime   time.Time `json:"end_time"`   // Full timestamp
	KWh       float64   `json:"kwh"`
	Service   string    `json:"service"` // "nyseg" or "coned"
}

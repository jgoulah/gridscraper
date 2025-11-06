package models

import "time"

// UsageData represents a single day's electricity usage
type UsageData struct {
	ID      int       `json:"id"`
	Date    time.Time `json:"date"`
	KWh     float64   `json:"kwh"`
	Service string    `json:"service"` // "nyseg" or "coned"
}

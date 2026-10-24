package model

// TimingRecordSummary is one entry in a recent-builds listing.
type TimingRecordSummary struct {
	ID              string  `json:"id"`
	StartedAt       string  `json:"startedAt"`
	DurationSeconds float64 `json:"durationSeconds"`
	Failed          bool    `json:"failed"`
}

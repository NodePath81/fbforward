package iplog

import "time"

const (
	DefaultQueryLimit = 200
	MaxQueryLimit     = 1000
)

type CloseEvent struct {
	IP         string
	Protocol   string
	Upstream   string
	Port       int
	BytesUp    uint64
	BytesDown  uint64
	DurationMs int64
	RecordedAt time.Time
}

type EnrichedRecord struct {
	CloseEvent
	ASN     int
	ASOrg   string
	Country string
}

type Record struct {
	ID         int64  `json:"id"`
	IP         string `json:"ip"`
	ASN        int    `json:"asn"`
	ASOrg      string `json:"as_org"`
	Country    string `json:"country"`
	Protocol   string `json:"protocol"`
	Upstream   string `json:"upstream"`
	Port       int    `json:"port"`
	BytesUp    uint64 `json:"bytes_up"`
	BytesDown  uint64 `json:"bytes_down"`
	DurationMs int64  `json:"duration_ms"`
	RecordedAt int64  `json:"recorded_at"`
}

type QueryParams struct {
	StartTime *int64
	EndTime   *int64
	CIDR      string
	ASN       *int
	Country   string
	SortBy    string
	SortOrder string
	Limit     int
	Offset    int
}

type QueryResult struct {
	Total   int      `json:"total"`
	Records []Record `json:"records"`
}

type StoreStats struct {
	RecordCount    int   `json:"record_count"`
	OldestRecordAt int64 `json:"oldest_record_at"`
	NewestRecordAt int64 `json:"newest_record_at"`
}

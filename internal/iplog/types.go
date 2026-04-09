package iplog

import "time"

const (
	DefaultQueryLimit = 200
	MaxQueryLimit     = 1000
)

const (
	EntryTypeAll       = "all"
	EntryTypeFlow      = "flow"
	EntryTypeRejection = "rejection"
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

type RejectionEvent struct {
	IP               string
	Protocol         string
	Port             int
	Reason           string
	MatchedRuleType  string
	MatchedRuleValue string
	RecordedAt       time.Time
}

type EnrichedRejectionRecord struct {
	RejectionEvent
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

type RejectionRecord struct {
	ID               int64  `json:"id"`
	IP               string `json:"ip"`
	ASN              int    `json:"asn"`
	ASOrg            string `json:"as_org"`
	Country          string `json:"country"`
	Protocol         string `json:"protocol"`
	Port             int    `json:"port"`
	Reason           string `json:"reason"`
	MatchedRuleType  string `json:"matched_rule_type"`
	MatchedRuleValue string `json:"matched_rule_value"`
	RecordedAt       int64  `json:"recorded_at"`
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

type RejectionQueryParams struct {
	StartTime        *int64
	EndTime          *int64
	CIDR             string
	ASN              *int
	Country          string
	Reason           string
	Protocol         string
	Port             *int
	MatchedRuleType  string
	MatchedRuleValue string
	SortBy           string
	SortOrder        string
	Limit            int
	Offset           int
}

type QueryResult struct {
	Total   int      `json:"total"`
	Records []Record `json:"records"`
}

type RejectionQueryResult struct {
	Total   int               `json:"total"`
	Records []RejectionRecord `json:"records"`
}

type LogEventQueryParams struct {
	StartTime        *int64
	EndTime          *int64
	CIDR             string
	ASN              *int
	Country          string
	Protocol         string
	Port             *int
	Reason           string
	MatchedRuleType  string
	MatchedRuleValue string
	EntryType        string
	SortBy           string
	SortOrder        string
	Limit            int
	Offset           int
}

type LogEventRecord struct {
	EntryType        string  `json:"entry_type"`
	IP               string  `json:"ip"`
	ASN              int     `json:"asn"`
	ASOrg            string  `json:"as_org"`
	Country          string  `json:"country"`
	Protocol         string  `json:"protocol"`
	Port             int     `json:"port"`
	RecordedAt       int64   `json:"recorded_at"`
	Upstream         *string `json:"upstream"`
	BytesUp          *uint64 `json:"bytes_up"`
	BytesDown        *uint64 `json:"bytes_down"`
	DurationMs       *int64  `json:"duration_ms"`
	Reason           *string `json:"reason"`
	MatchedRuleType  *string `json:"matched_rule_type"`
	MatchedRuleValue *string `json:"matched_rule_value"`

	sourceID int64
}

type LogEventQueryResult struct {
	Total   int              `json:"total"`
	Records []LogEventRecord `json:"records"`
}

type StoreStats struct {
	FlowRecordCount      int   `json:"flow_record_count"`
	RejectionRecordCount int   `json:"rejection_record_count"`
	TotalRecordCount     int   `json:"total_record_count"`
	OldestRecordAt       int64 `json:"oldest_record_at"`
	NewestRecordAt       int64 `json:"newest_record_at"`
}

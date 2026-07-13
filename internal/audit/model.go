package audit

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

// CloseEvent is the compatibility event accepted by the existing iplog
// pipeline. New code should prefer FlowRecord, which has explicit address
// components and lifecycle timestamps.
type CloseEvent struct {
	FlowID        string
	IP            string
	ClientPort    int
	Protocol      string
	Listener      string
	Route         string
	Upstream      string
	Port          int
	BytesUp       uint64
	BytesDown     uint64
	DurationMs    int64
	StartedAt     time.Time
	EndedAt       time.Time
	LastActivity  time.Time
	CloseReason   string
	Fingerprint   string
	PolicyVersion string
	RuleID        string
	RecordedAt    time.Time
}

type EnrichedRecord struct {
	CloseEvent
	ASN     int
	ASOrg   string
	Country string
}

type RejectionEvent struct {
	EventID          string
	IP               string
	ClientPort       int
	Protocol         string
	Listener         string
	Port             int
	Reason           string
	MatchedRuleType  string
	MatchedRuleValue string
	PolicyVersion    string
	RuleID           string
	RecordedAt       time.Time
}

type EnrichedRejectionRecord struct {
	RejectionEvent
	ASN     int
	ASOrg   string
	Country string
}

// FlowRecord is the durable lifecycle row. Times are represented as UTC
// time.Time in Go and stored as Unix milliseconds in SQLite.
type FlowRecord struct {
	FlowID        string
	Protocol      string
	ClientIP      string
	ClientPort    int
	Listener      string
	Route         string
	Upstream      string
	StartedAt     time.Time
	EndedAt       time.Time
	LastActivity  time.Time
	BytesUp       uint64
	BytesDown     uint64
	CloseReason   string
	Fingerprint   string
	PolicyVersion string
	RuleID        string
	ASN           int
	ASOrg         string
	Country       string
}

// FlowEntity is the durable identity/context row created when a Flow opens.
// It is deliberately separate from FlowRecord: flows is reserved for the
// complete lifecycle summary written at close time.
type FlowEntity struct {
	FlowID          string
	Protocol        string
	ClientIP        string
	ClientPort      int
	Listener        string
	Route           string
	Upstream        string
	BackendKey      string
	BackendProtocol string
	BackendLocal    string
	BackendRemote   string
	CreatedAt       time.Time
	EndedAt         *time.Time
	ResolveUntil    *time.Time
	State           string
	Generation      uint64
	LastActivity    time.Time
	BytesUp         uint64
	BytesDown       uint64
}

type FlowCheckpoint struct {
	FlowID       string
	RecordedAt   time.Time
	LastActivity time.Time
	BytesUp      uint64
	BytesDown    uint64
	SegmentsUp   uint64
	SegmentsDown uint64
}

type RejectionRow struct {
	EventID          string
	Protocol         string
	ClientIP         string
	ClientPort       int
	Listener         string
	Port             int
	Reason           string
	MatchedRuleType  string
	MatchedRuleValue string
	PolicyVersion    string
	RuleID           string
	RecordedAt       time.Time
	ASN              int
	ASOrg            string
	Country          string
}

type FlowTagEvent struct {
	EventID   string
	FlowID    string
	Tag       string
	Operation string
	Source    string
	Actor     string
	ExpiresAt *time.Time
	CreatedAt time.Time
	Metadata  string
}

type FlowTag struct {
	FlowID    string
	Tag       string
	Source    string
	ExpiresAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ClientTag struct {
	ClientIP  string
	Tag       string
	Source    string
	ExpiresAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

type OnlineRule struct {
	RuleID      string
	Version     string
	Action      string
	RuleType    string
	RuleValue   string
	Protocol    string
	Port        *int
	Priority    int
	Enabled     bool
	ExpiresAt   *time.Time
	Source      string
	CreatedBy   string
	Reason      string
	TicketRef   string
	MatcherJSON string
	ParamsJSON  string
	PayloadJSON string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type OnlineRuleEvent struct {
	EventID     string
	RuleID      string
	Operation   string
	Action      string
	Actor       string
	Reason      string
	TicketRef   string
	PayloadJSON string
	OccurredAt  time.Time
}

type PolicyEvent struct {
	EventID       string
	FlowID        string
	ClientIP      string
	PolicyVersion string
	RuleID        string
	Decision      string
	RuleType      string
	RuleValue     string
	Reason        string
	OccurredAt    time.Time
}

// Compatibility query types. Their JSON shape intentionally matches the
// existing /rpc responses consumed by management clients.
type Record struct {
	ID          int64  `json:"id"`
	FlowID      string `json:"flow_id,omitempty"`
	IP          string `json:"ip"`
	ASN         int    `json:"asn"`
	ASOrg       string `json:"as_org"`
	Country     string `json:"country"`
	Protocol    string `json:"protocol"`
	Upstream    string `json:"upstream"`
	Listener    string `json:"listener,omitempty"`
	Route       string `json:"route,omitempty"`
	Port        int    `json:"port"`
	BytesUp     uint64 `json:"bytes_up"`
	BytesDown   uint64 `json:"bytes_down"`
	DurationMs  int64  `json:"duration_ms"`
	StartedAt   int64  `json:"started_at,omitempty"`
	EndedAt     int64  `json:"ended_at,omitempty"`
	CloseReason string `json:"close_reason,omitempty"`
	RecordedAt  int64  `json:"recorded_at"`
}

type RejectionRecordResult struct {
	ID               int64  `json:"id"`
	EventID          string `json:"event_id,omitempty"`
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

// RejectionRecord is kept as the old public name.
type RejectionRecord = RejectionRecordResult

type QueryParams struct {
	StartTime *int64
	EndTime   *int64
	CIDR      string
	IP        string
	ASN       *int
	Country   string
	Tag       string
	Protocol  string
	Upstream  string
	SortBy    string
	SortOrder string
	Limit     int
	Offset    int
}

type RejectionQueryParams struct {
	StartTime        *int64
	EndTime          *int64
	CIDR             string
	IP               string
	ASN              *int
	Country          string
	Tag              string
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
	IP               string
	ASN              *int
	Country          string
	Tag              string
	Protocol         string
	Upstream         string
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
	FlowID           *string `json:"flow_id,omitempty"`
	Listener         *string `json:"listener,omitempty"`
	Route            *string `json:"route,omitempty"`
	CloseReason      *string `json:"close_reason,omitempty"`

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

type TopTalker struct {
	ClientIP   string `json:"client_ip"`
	BytesUp    uint64 `json:"bytes_up"`
	BytesDown  uint64 `json:"bytes_down"`
	BytesTotal uint64 `json:"bytes_total"`
	FlowCount  int    `json:"flow_count"`
}

type TopTalkerParams struct {
	StartTime *int64
	EndTime   *int64
	Protocol  string
	Upstream  string
	Tag       string
	SortBy    string
	SortOrder string
	Limit     int
	Offset    int
}

type TopASN struct {
	ASN        int    `json:"asn"`
	ASOrg      string `json:"as_org"`
	Country    string `json:"country"`
	BytesUp    uint64 `json:"bytes_up"`
	BytesDown  uint64 `json:"bytes_down"`
	BytesTotal uint64 `json:"bytes_total"`
	FlowCount  int    `json:"flow_count"`
}

type TopASNParams struct {
	StartTime *int64
	EndTime   *int64
	Protocol  string
	Upstream  string
	Tag       string
	SortBy    string
	SortOrder string
	Limit     int
	Offset    int
}

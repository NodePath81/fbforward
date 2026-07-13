package iplog

import "github.com/NodePath81/fbforward/internal/audit"

const (
	DefaultQueryLimit  = audit.DefaultQueryLimit
	MaxQueryLimit      = audit.MaxQueryLimit
	EntryTypeAll       = audit.EntryTypeAll
	EntryTypeFlow      = audit.EntryTypeFlow
	EntryTypeRejection = audit.EntryTypeRejection
)

type CloseEvent = audit.CloseEvent
type EnrichedRecord = audit.EnrichedRecord
type RejectionEvent = audit.RejectionEvent
type EnrichedRejectionRecord = audit.EnrichedRejectionRecord
type Record = audit.Record
type RejectionRecord = audit.RejectionRecord
type QueryParams = audit.QueryParams
type RejectionQueryParams = audit.RejectionQueryParams
type QueryResult = audit.QueryResult
type RejectionQueryResult = audit.RejectionQueryResult
type LogEventQueryParams = audit.LogEventQueryParams
type LogEventRecord = audit.LogEventRecord
type LogEventQueryResult = audit.LogEventQueryResult
type StoreStats = audit.StoreStats
type TopTalker = audit.TopTalker
type TopTalkerParams = audit.TopTalkerParams
type TopASN = audit.TopASN
type TopASNParams = audit.TopASNParams

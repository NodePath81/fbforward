package iplog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var flowSortColumns = map[string]string{
	"recorded_at": "recorded_at",
	"bytes_up":    "bytes_up",
	"bytes_down":  "bytes_down",
	"bytes_total": "(bytes_up + bytes_down)",
	"duration_ms": "duration_ms",
}

var rejectionSortKeys = map[string]struct{}{
	"recorded_at":        {},
	"ip":                 {},
	"asn":                {},
	"country":            {},
	"protocol":           {},
	"port":               {},
	"reason":             {},
	"matched_rule_type":  {},
	"matched_rule_value": {},
}

var logEventSortKeysCommon = map[string]struct{}{
	"recorded_at": {},
	"ip":          {},
	"asn":         {},
	"country":     {},
	"protocol":    {},
	"port":        {},
	"entry_type":  {},
}

var logEventSortKeysFlow = map[string]struct{}{
	"recorded_at": {},
	"ip":          {},
	"asn":         {},
	"country":     {},
	"protocol":    {},
	"port":        {},
	"entry_type":  {},
	"upstream":    {},
	"bytes_up":    {},
	"bytes_down":  {},
	"bytes_total": {},
	"duration_ms": {},
}

var logEventSortKeysRejection = map[string]struct{}{
	"recorded_at":        {},
	"ip":                 {},
	"asn":                {},
	"country":            {},
	"protocol":           {},
	"port":               {},
	"entry_type":         {},
	"reason":             {},
	"matched_rule_type":  {},
	"matched_rule_value": {},
}

const ipLogSchema = `
CREATE TABLE IF NOT EXISTS ip_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ip TEXT NOT NULL,
    asn INTEGER,
    as_org TEXT,
    country TEXT,
    protocol TEXT NOT NULL,
    upstream TEXT NOT NULL,
    port INTEGER NOT NULL,
    bytes_up INTEGER NOT NULL,
    bytes_down INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    recorded_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ip_log_ip ON ip_log(ip);
CREATE INDEX IF NOT EXISTS idx_ip_log_recorded_at ON ip_log(recorded_at);

CREATE TABLE IF NOT EXISTS rejection_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ip TEXT NOT NULL,
    asn INTEGER,
    as_org TEXT,
    country TEXT,
    protocol TEXT NOT NULL,
    port INTEGER NOT NULL,
    reason TEXT NOT NULL,
    matched_rule_type TEXT,
    matched_rule_value TEXT,
    recorded_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rejection_log_ip ON rejection_log(ip);
CREATE INDEX IF NOT EXISTS idx_rejection_log_recorded_at ON rejection_log(recorded_at);
`

type Store struct {
	writeDB *sql.DB
	readDB  *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	writeDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	if _, err := writeDB.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	if _, err := writeDB.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	if _, err := writeDB.Exec(ipLogSchema); err != nil {
		_ = writeDB.Close()
		return nil, err
	}

	readDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(1)
	readDB.SetMaxIdleConns(1)
	if _, err := readDB.Exec(`PRAGMA query_only = true;`); err != nil {
		_ = writeDB.Close()
		_ = readDB.Close()
		return nil, err
	}
	if _, err := readDB.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		_ = writeDB.Close()
		_ = readDB.Close()
		return nil, err
	}

	return &Store{
		writeDB: writeDB,
		readDB:  readDB,
	}, nil
}

func (s *Store) InsertBatch(records []EnrichedRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO ip_log (
			ip, asn, as_org, country, protocol, upstream, port,
			bytes_up, bytes_down, duration_ms, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, record := range records {
		if _, err := stmt.Exec(
			record.IP,
			record.ASN,
			record.ASOrg,
			record.Country,
			record.Protocol,
			record.Upstream,
			record.Port,
			record.BytesUp,
			record.BytesDown,
			record.DurationMs,
			record.RecordedAt.Unix(),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) InsertRejectionBatch(records []EnrichedRejectionRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO rejection_log (
			ip, asn, as_org, country, protocol, port, reason,
			matched_rule_type, matched_rule_value, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, record := range records {
		if _, err := stmt.Exec(
			record.IP,
			record.ASN,
			record.ASOrg,
			record.Country,
			record.Protocol,
			record.Port,
			record.Reason,
			nullIfEmpty(record.MatchedRuleType),
			nullIfEmpty(record.MatchedRuleValue),
			record.RecordedAt.Unix(),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Prune(olderThan time.Time) (int64, error) {
	flowResult, err := s.writeDB.Exec(`DELETE FROM ip_log WHERE recorded_at < ?`, olderThan.Unix())
	if err != nil {
		return 0, err
	}
	rejectionResult, err := s.writeDB.Exec(`DELETE FROM rejection_log WHERE recorded_at < ?`, olderThan.Unix())
	if err != nil {
		return 0, err
	}
	flowDeleted, err := flowResult.RowsAffected()
	if err != nil {
		return 0, err
	}
	rejectionDeleted, err := rejectionResult.RowsAffected()
	if err != nil {
		return 0, err
	}
	return flowDeleted + rejectionDeleted, nil
}

func (s *Store) Stats() (StoreStats, error) {
	var stats StoreStats
	if err := s.readDB.QueryRow(`SELECT COUNT(*), COALESCE(MIN(recorded_at), 0), COALESCE(MAX(recorded_at), 0) FROM ip_log`).Scan(
		&stats.FlowRecordCount,
		&stats.OldestRecordAt,
		&stats.NewestRecordAt,
	); err != nil {
		return StoreStats{}, err
	}

	var (
		rejectionOldest int64
		rejectionNewest int64
	)
	if err := s.readDB.QueryRow(`SELECT COUNT(*), COALESCE(MIN(recorded_at), 0), COALESCE(MAX(recorded_at), 0) FROM rejection_log`).Scan(
		&stats.RejectionRecordCount,
		&rejectionOldest,
		&rejectionNewest,
	); err != nil {
		return StoreStats{}, err
	}

	stats.TotalRecordCount = stats.FlowRecordCount + stats.RejectionRecordCount
	stats.OldestRecordAt = minPositive(stats.OldestRecordAt, rejectionOldest)
	stats.NewestRecordAt = maxPositive(stats.NewestRecordAt, rejectionNewest)
	return stats, nil
}

func (s *Store) StartRetention(ctx context.Context, retention, pruneEvery time.Duration) {
	if s == nil || retention <= 0 {
		return
	}
	if pruneEvery <= 0 {
		pruneEvery = time.Hour
	}
	_, _ = s.Prune(time.Now().Add(-retention))
	ticker := time.NewTicker(pruneEvery)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.Prune(time.Now().Add(-retention))
			}
		}
	}()
}

func (s *Store) Query(params QueryParams) (QueryResult, error) {
	normalized, err := NormalizeQueryParams(params)
	if err != nil {
		return QueryResult{}, err
	}

	if normalized.CIDR != "" {
		return s.queryWithCIDR(normalized)
	}
	return s.queryWithoutCIDR(normalized)
}

func (s *Store) QueryRejections(params RejectionQueryParams) (RejectionQueryResult, error) {
	normalized, err := NormalizeRejectionQueryParams(params)
	if err != nil {
		return RejectionQueryResult{}, err
	}

	records, err := s.loadRejectionRecords(normalized)
	if err != nil {
		return RejectionQueryResult{}, err
	}
	if normalized.CIDR != "" {
		records = filterRejectionRecordsByCIDR(records, normalized.CIDR)
	}
	sortRejectionRecords(records, normalized)
	return paginateRejectionRecords(records, normalized), nil
}

func (s *Store) QueryLogEvents(params LogEventQueryParams) (LogEventQueryResult, error) {
	normalized, err := NormalizeLogEventQueryParams(params)
	if err != nil {
		return LogEventQueryResult{}, err
	}

	records := make([]LogEventRecord, 0)
	if normalized.EntryType == EntryTypeAll || normalized.EntryType == EntryTypeFlow {
		flows, err := s.loadFlowRecordsForEvents(normalized)
		if err != nil {
			return LogEventQueryResult{}, err
		}
		records = append(records, flows...)
	}
	if normalized.EntryType == EntryTypeAll || normalized.EntryType == EntryTypeRejection {
		rejections, err := s.loadRejectionRecordsForEvents(normalized)
		if err != nil {
			return LogEventQueryResult{}, err
		}
		records = append(records, rejections...)
	}
	if normalized.CIDR != "" {
		records = filterLogEventRecordsByCIDR(records, normalized.CIDR)
	}
	sortLogEventRecords(records, normalized)
	return paginateLogEventRecords(records, normalized), nil
}

func (s *Store) queryWithoutCIDR(params QueryParams) (QueryResult, error) {
	where, args := buildWhereClause(params)
	countQuery := `SELECT COUNT(*) FROM ip_log` + where
	var total int
	if err := s.readDB.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return QueryResult{}, err
	}

	query := `SELECT id, ip, asn, as_org, country, protocol, upstream, port, bytes_up, bytes_down, duration_ms, recorded_at FROM ip_log` + where + flowOrderClause(params) + ` LIMIT ? OFFSET ?`
	args = append(args, params.Limit, params.Offset)
	rows, err := s.readDB.Query(query, args...)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()

	records, err := scanRecords(rows)
	if err != nil {
		return QueryResult{}, err
	}
	return QueryResult{Total: total, Records: records}, nil
}

func (s *Store) queryWithCIDR(params QueryParams) (QueryResult, error) {
	where, args := buildWhereClause(QueryParams{
		StartTime: params.StartTime,
		EndTime:   params.EndTime,
		ASN:       params.ASN,
		Country:   params.Country,
	})
	query := `SELECT id, ip, asn, as_org, country, protocol, upstream, port, bytes_up, bytes_down, duration_ms, recorded_at FROM ip_log` + where
	rows, err := s.readDB.Query(query, args...)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()

	all, err := scanRecords(rows)
	if err != nil {
		return QueryResult{}, err
	}
	filtered := filterFlowRecordsByCIDR(all, params.CIDR)
	sortRecords(filtered, params)
	return paginateFlowRecords(filtered, params), nil
}

func (s *Store) loadFlowRecordsForEvents(params LogEventQueryParams) ([]LogEventRecord, error) {
	where, args := buildWhereClause(QueryParams{
		StartTime: params.StartTime,
		EndTime:   params.EndTime,
		ASN:       params.ASN,
		Country:   params.Country,
	})
	query := `SELECT id, ip, asn, as_org, country, protocol, upstream, port, bytes_up, bytes_down, duration_ms, recorded_at FROM ip_log` + where
	rows, err := s.readDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	flowRecords, err := scanRecords(rows)
	if err != nil {
		return nil, err
	}
	out := make([]LogEventRecord, 0, len(flowRecords))
	for _, record := range flowRecords {
		if params.Protocol != "" && record.Protocol != params.Protocol {
			continue
		}
		if params.Port != nil && record.Port != *params.Port {
			continue
		}
		out = append(out, flowRecordToLogEvent(record))
	}
	return out, nil
}

func (s *Store) loadRejectionRecords(params RejectionQueryParams) ([]RejectionRecord, error) {
	where, args := buildRejectionWhereClause(params)
	query := `SELECT id, ip, asn, as_org, country, protocol, port, reason, matched_rule_type, matched_rule_value, recorded_at FROM rejection_log` + where
	rows, err := s.readDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRejectionRecords(rows)
}

func (s *Store) loadRejectionRecordsForEvents(params LogEventQueryParams) ([]LogEventRecord, error) {
	rejectionParams := RejectionQueryParams{
		StartTime:        params.StartTime,
		EndTime:          params.EndTime,
		ASN:              params.ASN,
		Country:          params.Country,
		Reason:           params.Reason,
		Protocol:         params.Protocol,
		Port:             params.Port,
		MatchedRuleType:  params.MatchedRuleType,
		MatchedRuleValue: params.MatchedRuleValue,
		Limit:            DefaultQueryLimit,
	}
	rejectionRecords, err := s.loadRejectionRecords(rejectionParams)
	if err != nil {
		return nil, err
	}
	out := make([]LogEventRecord, 0, len(rejectionRecords))
	for _, record := range rejectionRecords {
		out = append(out, rejectionRecordToLogEvent(record))
	}
	return out, nil
}

func (s *Store) Close() error {
	var errs []error
	if s.writeDB != nil {
		if err := s.writeDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.readDB != nil {
		if err := s.readDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func NormalizeQueryParams(params QueryParams) (QueryParams, error) {
	out := params
	if err := normalizeCommonQuery(&out.StartTime, &out.EndTime, &out.CIDR, &out.Country, &out.Limit, &out.Offset); err != nil {
		return QueryParams{}, err
	}
	out.SortBy = strings.TrimSpace(out.SortBy)
	if out.SortBy == "" {
		out.SortBy = "recorded_at"
	}
	if _, ok := flowSortColumns[out.SortBy]; !ok {
		return QueryParams{}, errors.New("invalid sort_by: must be one of recorded_at, bytes_up, bytes_down, bytes_total, duration_ms")
	}
	out.SortOrder = normalizeSortOrder(out.SortOrder)
	if out.SortOrder == "" {
		return QueryParams{}, errors.New("invalid sort_order: must be asc or desc")
	}
	return out, nil
}

func NormalizeRejectionQueryParams(params RejectionQueryParams) (RejectionQueryParams, error) {
	out := params
	if err := normalizeCommonQuery(&out.StartTime, &out.EndTime, &out.CIDR, &out.Country, &out.Limit, &out.Offset); err != nil {
		return RejectionQueryParams{}, err
	}
	out.Reason = strings.TrimSpace(out.Reason)
	out.Protocol = strings.ToLower(strings.TrimSpace(out.Protocol))
	if out.Protocol != "" && out.Protocol != "tcp" && out.Protocol != "udp" {
		return RejectionQueryParams{}, errors.New("protocol must be tcp or udp")
	}
	if out.Port != nil && *out.Port <= 0 {
		return RejectionQueryParams{}, errors.New("port must be > 0")
	}
	out.MatchedRuleType = strings.TrimSpace(out.MatchedRuleType)
	out.MatchedRuleValue = strings.TrimSpace(out.MatchedRuleValue)
	out.SortBy = strings.TrimSpace(out.SortBy)
	if out.SortBy == "" {
		out.SortBy = "recorded_at"
	}
	if _, ok := rejectionSortKeys[out.SortBy]; !ok {
		return RejectionQueryParams{}, errors.New("invalid sort_by: must be one of recorded_at, ip, asn, country, protocol, port, reason, matched_rule_type, matched_rule_value")
	}
	out.SortOrder = normalizeSortOrder(out.SortOrder)
	if out.SortOrder == "" {
		return RejectionQueryParams{}, errors.New("invalid sort_order: must be asc or desc")
	}
	return out, nil
}

func NormalizeLogEventQueryParams(params LogEventQueryParams) (LogEventQueryParams, error) {
	out := params
	if err := normalizeCommonQuery(&out.StartTime, &out.EndTime, &out.CIDR, &out.Country, &out.Limit, &out.Offset); err != nil {
		return LogEventQueryParams{}, err
	}
	out.Protocol = strings.ToLower(strings.TrimSpace(out.Protocol))
	if out.Protocol != "" && out.Protocol != "tcp" && out.Protocol != "udp" {
		return LogEventQueryParams{}, errors.New("protocol must be tcp or udp")
	}
	if out.Port != nil && *out.Port <= 0 {
		return LogEventQueryParams{}, errors.New("port must be > 0")
	}
	out.Reason = strings.TrimSpace(out.Reason)
	out.MatchedRuleType = strings.TrimSpace(out.MatchedRuleType)
	out.MatchedRuleValue = strings.TrimSpace(out.MatchedRuleValue)
	out.EntryType = strings.ToLower(strings.TrimSpace(out.EntryType))
	if out.EntryType == "" {
		out.EntryType = EntryTypeAll
	}
	switch out.EntryType {
	case EntryTypeAll, EntryTypeFlow, EntryTypeRejection:
	default:
		return LogEventQueryParams{}, errors.New("entry_type must be one of all, flow, rejection")
	}
	out.SortBy = strings.TrimSpace(out.SortBy)
	if out.SortBy == "" {
		out.SortBy = "recorded_at"
	}
	if _, ok := allowedLogEventSortKeys(out.EntryType)[out.SortBy]; !ok {
		return LogEventQueryParams{}, fmt.Errorf("invalid sort_by for entry_type %s", out.EntryType)
	}
	out.SortOrder = normalizeSortOrder(out.SortOrder)
	if out.SortOrder == "" {
		return LogEventQueryParams{}, errors.New("invalid sort_order: must be asc or desc")
	}
	return out, nil
}

func normalizeCommonQuery(startTime, endTime **int64, cidr, country *string, limit, offset *int) error {
	if *limit == 0 {
		*limit = DefaultQueryLimit
	}
	if *limit < 0 || *limit > MaxQueryLimit {
		return fmt.Errorf("limit must be in 1..%d", MaxQueryLimit)
	}
	if *offset < 0 {
		return errors.New("offset must be >= 0")
	}
	*country = strings.ToUpper(strings.TrimSpace(*country))
	*cidr = strings.TrimSpace(*cidr)
	if *cidr != "" && *startTime == nil && *endTime == nil {
		return errors.New("cidr filter requires start_time or end_time")
	}
	if *startTime != nil && *endTime != nil && **endTime < **startTime {
		return errors.New("end_time must be >= start_time")
	}
	if *cidr != "" {
		if _, _, err := net.ParseCIDR(*cidr); err != nil {
			return fmt.Errorf("invalid cidr: %w", err)
		}
	}
	return nil
}

func normalizeSortOrder(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "desc"
	}
	if value != "asc" && value != "desc" {
		return ""
	}
	return value
}

func allowedLogEventSortKeys(entryType string) map[string]struct{} {
	switch entryType {
	case EntryTypeFlow:
		return logEventSortKeysFlow
	case EntryTypeRejection:
		return logEventSortKeysRejection
	default:
		return logEventSortKeysCommon
	}
}

func flowOrderClause(params QueryParams) string {
	direction := "DESC"
	if params.SortOrder == "asc" {
		direction = "ASC"
	}
	return fmt.Sprintf(" ORDER BY %s %s, id %s", flowSortColumns[params.SortBy], direction, direction)
}

func sortRecords(records []Record, params QueryParams) {
	sort.Slice(records, func(i, j int) bool {
		return lessRecord(records[i], records[j], params)
	})
}

func lessRecord(a, b Record, params QueryParams) bool {
	desc := params.SortOrder == "desc"
	var less bool
	var greater bool
	switch params.SortBy {
	case "recorded_at":
		less = a.RecordedAt < b.RecordedAt
		greater = a.RecordedAt > b.RecordedAt
	case "bytes_up":
		less = a.BytesUp < b.BytesUp
		greater = a.BytesUp > b.BytesUp
	case "bytes_down":
		less = a.BytesDown < b.BytesDown
		greater = a.BytesDown > b.BytesDown
	case "bytes_total":
		aTotal := a.BytesUp + a.BytesDown
		bTotal := b.BytesUp + b.BytesDown
		less = aTotal < bTotal
		greater = aTotal > bTotal
	case "duration_ms":
		less = a.DurationMs < b.DurationMs
		greater = a.DurationMs > b.DurationMs
	default:
		less = a.RecordedAt < b.RecordedAt
		greater = a.RecordedAt > b.RecordedAt
	}
	if less || greater {
		if desc {
			return greater
		}
		return less
	}
	if desc {
		return a.ID > b.ID
	}
	return a.ID < b.ID
}

func sortRejectionRecords(records []RejectionRecord, params RejectionQueryParams) {
	sort.Slice(records, func(i, j int) bool {
		return lessRejectionRecord(records[i], records[j], params)
	})
}

func lessRejectionRecord(a, b RejectionRecord, params RejectionQueryParams) bool {
	desc := params.SortOrder == "desc"
	var cmp int
	switch params.SortBy {
	case "recorded_at":
		cmp = compareInt64(a.RecordedAt, b.RecordedAt)
	case "ip":
		cmp = compareString(a.IP, b.IP)
	case "asn":
		cmp = compareInt(a.ASN, b.ASN)
	case "country":
		cmp = compareString(a.Country, b.Country)
	case "protocol":
		cmp = compareString(a.Protocol, b.Protocol)
	case "port":
		cmp = compareInt(a.Port, b.Port)
	case "reason":
		cmp = compareString(a.Reason, b.Reason)
	case "matched_rule_type":
		cmp = compareString(a.MatchedRuleType, b.MatchedRuleType)
	case "matched_rule_value":
		cmp = compareString(a.MatchedRuleValue, b.MatchedRuleValue)
	default:
		cmp = compareInt64(a.RecordedAt, b.RecordedAt)
	}
	if cmp != 0 {
		return pickCompare(cmp, desc)
	}
	if a.RecordedAt != b.RecordedAt {
		return pickCompare(compareInt64(a.RecordedAt, b.RecordedAt), desc)
	}
	if desc {
		return a.ID > b.ID
	}
	return a.ID < b.ID
}

func sortLogEventRecords(records []LogEventRecord, params LogEventQueryParams) {
	sort.Slice(records, func(i, j int) bool {
		return lessLogEventRecord(records[i], records[j], params)
	})
}

func lessLogEventRecord(a, b LogEventRecord, params LogEventQueryParams) bool {
	desc := params.SortOrder == "desc"
	var cmp int
	switch params.SortBy {
	case "recorded_at":
		cmp = compareInt64(a.RecordedAt, b.RecordedAt)
	case "ip":
		cmp = compareString(a.IP, b.IP)
	case "asn":
		cmp = compareInt(a.ASN, b.ASN)
	case "country":
		cmp = compareString(a.Country, b.Country)
	case "protocol":
		cmp = compareString(a.Protocol, b.Protocol)
	case "port":
		cmp = compareInt(a.Port, b.Port)
	case "entry_type":
		cmp = compareString(a.EntryType, b.EntryType)
	case "upstream":
		cmp = compareString(pointerString(a.Upstream), pointerString(b.Upstream))
	case "bytes_up":
		cmp = compareUint64(pointerUint64(a.BytesUp), pointerUint64(b.BytesUp))
	case "bytes_down":
		cmp = compareUint64(pointerUint64(a.BytesDown), pointerUint64(b.BytesDown))
	case "bytes_total":
		cmp = compareUint64(pointerUint64(a.BytesUp)+pointerUint64(a.BytesDown), pointerUint64(b.BytesUp)+pointerUint64(b.BytesDown))
	case "duration_ms":
		cmp = compareInt64(pointerInt64(a.DurationMs), pointerInt64(b.DurationMs))
	case "reason":
		cmp = compareString(pointerString(a.Reason), pointerString(b.Reason))
	case "matched_rule_type":
		cmp = compareString(pointerString(a.MatchedRuleType), pointerString(b.MatchedRuleType))
	case "matched_rule_value":
		cmp = compareString(pointerString(a.MatchedRuleValue), pointerString(b.MatchedRuleValue))
	default:
		cmp = compareInt64(a.RecordedAt, b.RecordedAt)
	}
	if cmp != 0 {
		return pickCompare(cmp, desc)
	}
	if a.RecordedAt != b.RecordedAt {
		return pickCompare(compareInt64(a.RecordedAt, b.RecordedAt), desc)
	}
	if a.EntryType != b.EntryType {
		return pickCompare(compareString(a.EntryType, b.EntryType), desc)
	}
	if desc {
		return a.sourceID > b.sourceID
	}
	return a.sourceID < b.sourceID
}

func buildWhereClause(params QueryParams) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 4)
	if params.StartTime != nil {
		clauses = append(clauses, "recorded_at >= ?")
		args = append(args, *params.StartTime)
	}
	if params.EndTime != nil {
		clauses = append(clauses, "recorded_at <= ?")
		args = append(args, *params.EndTime)
	}
	if params.ASN != nil {
		clauses = append(clauses, "asn = ?")
		args = append(args, *params.ASN)
	}
	if params.Country != "" {
		clauses = append(clauses, "country = ?")
		args = append(args, params.Country)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func buildRejectionWhereClause(params RejectionQueryParams) (string, []any) {
	clauses := make([]string, 0, 8)
	args := make([]any, 0, 8)
	if params.StartTime != nil {
		clauses = append(clauses, "recorded_at >= ?")
		args = append(args, *params.StartTime)
	}
	if params.EndTime != nil {
		clauses = append(clauses, "recorded_at <= ?")
		args = append(args, *params.EndTime)
	}
	if params.ASN != nil {
		clauses = append(clauses, "asn = ?")
		args = append(args, *params.ASN)
	}
	if params.Country != "" {
		clauses = append(clauses, "country = ?")
		args = append(args, params.Country)
	}
	if params.Reason != "" {
		clauses = append(clauses, "reason = ?")
		args = append(args, params.Reason)
	}
	if params.Protocol != "" {
		clauses = append(clauses, "protocol = ?")
		args = append(args, params.Protocol)
	}
	if params.Port != nil {
		clauses = append(clauses, "port = ?")
		args = append(args, *params.Port)
	}
	if params.MatchedRuleType != "" {
		clauses = append(clauses, "matched_rule_type = ?")
		args = append(args, params.MatchedRuleType)
	}
	if params.MatchedRuleValue != "" {
		clauses = append(clauses, "matched_rule_value = ?")
		args = append(args, params.MatchedRuleValue)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanRecords(rows *sql.Rows) ([]Record, error) {
	records := make([]Record, 0)
	for rows.Next() {
		var (
			record     Record
			bytesUp    int64
			bytesDown  int64
			recordedAt int64
			asOrg      sql.NullString
			country    sql.NullString
			asn        sql.NullInt64
		)
		if err := rows.Scan(
			&record.ID,
			&record.IP,
			&asn,
			&asOrg,
			&country,
			&record.Protocol,
			&record.Upstream,
			&record.Port,
			&bytesUp,
			&bytesDown,
			&record.DurationMs,
			&recordedAt,
		); err != nil {
			return nil, err
		}
		if asn.Valid {
			record.ASN = int(asn.Int64)
		}
		if asOrg.Valid {
			record.ASOrg = asOrg.String
		}
		if country.Valid {
			record.Country = country.String
		}
		record.BytesUp = uint64(bytesUp)
		record.BytesDown = uint64(bytesDown)
		record.RecordedAt = recordedAt
		records = append(records, record)
	}
	return records, rows.Err()
}

func scanRejectionRecords(rows *sql.Rows) ([]RejectionRecord, error) {
	records := make([]RejectionRecord, 0)
	for rows.Next() {
		var (
			record           RejectionRecord
			recordedAt       int64
			asOrg            sql.NullString
			country          sql.NullString
			asn              sql.NullInt64
			matchedRuleType  sql.NullString
			matchedRuleValue sql.NullString
		)
		if err := rows.Scan(
			&record.ID,
			&record.IP,
			&asn,
			&asOrg,
			&country,
			&record.Protocol,
			&record.Port,
			&record.Reason,
			&matchedRuleType,
			&matchedRuleValue,
			&recordedAt,
		); err != nil {
			return nil, err
		}
		if asn.Valid {
			record.ASN = int(asn.Int64)
		}
		if asOrg.Valid {
			record.ASOrg = asOrg.String
		}
		if country.Valid {
			record.Country = country.String
		}
		if matchedRuleType.Valid {
			record.MatchedRuleType = matchedRuleType.String
		}
		if matchedRuleValue.Valid {
			record.MatchedRuleValue = matchedRuleValue.String
		}
		record.RecordedAt = recordedAt
		records = append(records, record)
	}
	return records, rows.Err()
}

func paginateFlowRecords(records []Record, params QueryParams) QueryResult {
	total := len(records)
	start := params.Offset
	if start > total {
		start = total
	}
	end := start + params.Limit
	if end > total {
		end = total
	}
	return QueryResult{Total: total, Records: records[start:end]}
}

func paginateRejectionRecords(records []RejectionRecord, params RejectionQueryParams) RejectionQueryResult {
	total := len(records)
	start := params.Offset
	if start > total {
		start = total
	}
	end := start + params.Limit
	if end > total {
		end = total
	}
	return RejectionQueryResult{Total: total, Records: records[start:end]}
}

func paginateLogEventRecords(records []LogEventRecord, params LogEventQueryParams) LogEventQueryResult {
	total := len(records)
	start := params.Offset
	if start > total {
		start = total
	}
	end := start + params.Limit
	if end > total {
		end = total
	}
	return LogEventQueryResult{Total: total, Records: records[start:end]}
}

func filterFlowRecordsByCIDR(records []Record, cidr string) []Record {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	filtered := make([]Record, 0, len(records))
	for _, record := range records {
		ip := net.ParseIP(record.IP)
		if ip == nil {
			continue
		}
		if network.Contains(ip) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func filterRejectionRecordsByCIDR(records []RejectionRecord, cidr string) []RejectionRecord {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	filtered := make([]RejectionRecord, 0, len(records))
	for _, record := range records {
		ip := net.ParseIP(record.IP)
		if ip == nil {
			continue
		}
		if network.Contains(ip) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func filterLogEventRecordsByCIDR(records []LogEventRecord, cidr string) []LogEventRecord {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	filtered := make([]LogEventRecord, 0, len(records))
	for _, record := range records {
		ip := net.ParseIP(record.IP)
		if ip == nil {
			continue
		}
		if network.Contains(ip) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func flowRecordToLogEvent(record Record) LogEventRecord {
	upstream := record.Upstream
	bytesUp := record.BytesUp
	bytesDown := record.BytesDown
	durationMs := record.DurationMs
	return LogEventRecord{
		EntryType:  EntryTypeFlow,
		IP:         record.IP,
		ASN:        record.ASN,
		ASOrg:      record.ASOrg,
		Country:    record.Country,
		Protocol:   record.Protocol,
		Port:       record.Port,
		RecordedAt: record.RecordedAt,
		Upstream:   &upstream,
		BytesUp:    &bytesUp,
		BytesDown:  &bytesDown,
		DurationMs: &durationMs,
		sourceID:   record.ID,
	}
}

func rejectionRecordToLogEvent(record RejectionRecord) LogEventRecord {
	reason := record.Reason
	var matchedRuleType *string
	if record.MatchedRuleType != "" {
		value := record.MatchedRuleType
		matchedRuleType = &value
	}
	var matchedRuleValue *string
	if record.MatchedRuleValue != "" {
		value := record.MatchedRuleValue
		matchedRuleValue = &value
	}
	return LogEventRecord{
		EntryType:        EntryTypeRejection,
		IP:               record.IP,
		ASN:              record.ASN,
		ASOrg:            record.ASOrg,
		Country:          record.Country,
		Protocol:         record.Protocol,
		Port:             record.Port,
		RecordedAt:       record.RecordedAt,
		Reason:           &reason,
		MatchedRuleType:  matchedRuleType,
		MatchedRuleValue: matchedRuleValue,
		sourceID:         record.ID,
	}
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func minPositive(a, b int64) int64 {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

func maxPositive(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func compareString(a, b string) int {
	return strings.Compare(a, b)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func pickCompare(cmp int, desc bool) bool {
	if desc {
		return cmp > 0
	}
	return cmp < 0
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func pointerInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func pointerUint64(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

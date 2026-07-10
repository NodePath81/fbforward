package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const listenerPortSQL = "CAST(CASE WHEN instr(listener, ']:') > 0 THEN substr(listener, instr(listener, ']:') + 2) WHEN instr(listener, ':') > 0 THEN substr(listener, instr(listener, ':') + 1) ELSE '0' END AS INTEGER)"

var flowSortColumns = map[string]string{
	"recorded_at": "ended_at",
	"bytes_up":    "bytes_up",
	"bytes_down":  "bytes_down",
	"bytes_total": "(bytes_up + bytes_down)",
	"duration_ms": "(ended_at - started_at)",
}

var rejectionSortColumns = map[string]string{
	"recorded_at":        "recorded_at",
	"ip":                 "client_ip",
	"asn":                "asn",
	"country":            "country",
	"protocol":           "protocol",
	"port":               "port",
	"reason":             "reason",
	"matched_rule_type":  "matched_rule_type",
	"matched_rule_value": "matched_rule_value",
}

type Store struct {
	writeDB *sql.DB
	readDB  *sql.DB
	mu      sync.Mutex
}

func NewStore(dbPath string) (*Store, error) {
	writeDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	closeBoth := func() {
		_ = writeDB.Close()
	}
	for _, pragma := range []string{`PRAGMA journal_mode = WAL`, `PRAGMA busy_timeout = 5000`, `PRAGMA foreign_keys = ON`} {
		if _, err := writeDB.Exec(pragma); err != nil {
			closeBoth()
			return nil, fmt.Errorf("sqlite %s: %w", pragma, err)
		}
	}
	if err := migrateDB(writeDB); err != nil {
		closeBoth()
		return nil, err
	}

	readDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		closeBoth()
		return nil, err
	}
	readDB.SetMaxOpenConns(1)
	readDB.SetMaxIdleConns(1)
	for _, pragma := range []string{`PRAGMA query_only = true`, `PRAGMA busy_timeout = 5000`, `PRAGMA foreign_keys = ON`} {
		if _, err := readDB.Exec(pragma); err != nil {
			_ = writeDB.Close()
			_ = readDB.Close()
			return nil, fmt.Errorf("sqlite read %s: %w", pragma, err)
		}
	}
	return &Store{writeDB: writeDB, readDB: readDB}, nil
}

func (s *Store) InsertBatch(records []EnrichedRecord) error {
	flows := make([]FlowRecord, 0, len(records))
	for _, record := range records {
		flows = append(flows, flowRecordFromClose(record))
	}
	return s.InsertFlows(flows)
}

func (s *Store) InsertRejectionBatch(records []EnrichedRejectionRecord) error {
	rejections := make([]RejectionRow, 0, len(records))
	for _, record := range records {
		rejections = append(rejections, RejectionRow{
			EventID: record.EventID, Protocol: record.Protocol, ClientIP: record.IP,
			ClientPort: record.ClientPort, Listener: record.Listener, Port: record.Port,
			Reason: record.Reason, MatchedRuleType: record.MatchedRuleType,
			MatchedRuleValue: record.MatchedRuleValue, PolicyVersion: record.PolicyVersion,
			RuleID: record.RuleID, RecordedAt: record.RecordedAt, ASN: record.ASN,
			ASOrg: record.ASOrg, Country: record.Country,
		})
	}
	return s.InsertRejections(rejections)
}

func (s *Store) InsertFlows(records []FlowRecord) error {
	if s == nil || len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO flows(flow_id, protocol, client_ip, client_port, client_ip_bytes, client_ip_family, listener, route, upstream, started_at, ended_at, last_activity_at, bytes_up, bytes_down, close_reason, fingerprint, policy_version, rule_id, asn, as_org, country) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, record := range records {
		if record.FlowID == "" {
			record.FlowID = uuid.NewString()
		}
		if record.Protocol == "" {
			record.Protocol = "unknown"
		}
		blob, family := optionalIPBytes(record.ClientIP)
		started, ended, last := normalizeFlowTimes(record)
		if _, err := stmt.Exec(record.FlowID, record.Protocol, record.ClientIP, record.ClientPort, blob, family,
			record.Listener, record.Route, record.Upstream, unixMilli(started), unixMilli(ended), unixMilli(last),
			record.BytesUp, record.BytesDown, record.CloseReason, record.Fingerprint, record.PolicyVersion, record.RuleID,
			nullInt(record.ASN), nullIfEmpty(record.ASOrg), nullIfEmpty(record.Country)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) InsertRejections(records []RejectionRow) error {
	if s == nil || len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO rejection_events(event_id, protocol, client_ip, client_port, client_ip_bytes, client_ip_family, listener, port, reason, matched_rule_type, matched_rule_value, policy_version, rule_id, recorded_at, asn, as_org, country) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, record := range records {
		if record.EventID == "" {
			record.EventID = uuid.NewString()
		}
		blob, family := optionalIPBytes(record.ClientIP)
		if _, err := stmt.Exec(record.EventID, record.Protocol, record.ClientIP, record.ClientPort, blob, family,
			record.Listener, record.Port, record.Reason, record.MatchedRuleType, record.MatchedRuleValue,
			record.PolicyVersion, record.RuleID, unixMilli(defaultTime(record.RecordedAt)), nullInt(record.ASN),
			nullIfEmpty(record.ASOrg), nullIfEmpty(record.Country)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) InsertCheckpoints(records []FlowCheckpoint) error {
	if s == nil || len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO flow_checkpoints(flow_id, recorded_at, last_activity_at, bytes_up, bytes_down, segments_up, segments_down) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, record := range records {
		if _, err := stmt.Exec(record.FlowID, unixMilli(defaultTime(record.RecordedAt)), unixMilli(defaultTime(record.LastActivity)), record.BytesUp, record.BytesDown, record.SegmentsUp, record.SegmentsDown); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Prune(olderThan time.Time) (int64, error) {
	if s == nil {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return 0, err
	}
	cutoff := unixMilli(olderThan)
	var deleted int64
	for _, query := range []string{`DELETE FROM flows WHERE ended_at < ?`, `DELETE FROM rejection_events WHERE recorded_at < ?`, `DELETE FROM ip_log WHERE recorded_at < ?`, `DELETE FROM rejection_log WHERE recorded_at < ?`} {
		result, err := tx.Exec(query, cutoff)
		if strings.Contains(query, "ip_log") || strings.Contains(query, "rejection_log") {
			// Legacy tables use Unix seconds.
			result, err = tx.Exec(query, olderThan.Unix())
		}
		if err != nil {
			if strings.Contains(err.Error(), "no such table") {
				continue
			}
			_ = tx.Rollback()
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		deleted += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *Store) Stats() (StoreStats, error) {
	var stats StoreStats
	if err := s.readDB.QueryRow(`SELECT COUNT(*), COALESCE(MIN(ended_at), 0), COALESCE(MAX(ended_at), 0) FROM flows`).Scan(&stats.FlowRecordCount, &stats.OldestRecordAt, &stats.NewestRecordAt); err != nil {
		return StoreStats{}, err
	}
	var rejectionOldest, rejectionNewest int64
	if err := s.readDB.QueryRow(`SELECT COUNT(*), COALESCE(MIN(recorded_at), 0), COALESCE(MAX(recorded_at), 0) FROM rejection_events`).Scan(&stats.RejectionRecordCount, &rejectionOldest, &rejectionNewest); err != nil {
		return StoreStats{}, err
	}
	stats.TotalRecordCount = stats.FlowRecordCount + stats.RejectionRecordCount
	stats.OldestRecordAt = minPositive(stats.OldestRecordAt, rejectionOldest)
	stats.NewestRecordAt = maxPositive(stats.NewestRecordAt, rejectionNewest)
	stats.OldestRecordAt /= 1000
	stats.NewestRecordAt /= 1000
	return stats, nil
}

func (s *Store) Query(params QueryParams) (QueryResult, error) {
	p, err := normalizeQuery(params)
	if err != nil {
		return QueryResult{}, err
	}
	where, args, err := flowWhere(p)
	if err != nil {
		return QueryResult{}, err
	}
	var total int
	if err := s.readDB.QueryRow(`SELECT COUNT(*) FROM flows`+where, args...).Scan(&total); err != nil {
		return QueryResult{}, err
	}
	query := `SELECT id, flow_id, client_ip, asn, as_org, country, protocol, upstream, listener, route, ` + listenerPortSQL + `, bytes_up, bytes_down, (ended_at-started_at), started_at, ended_at, close_reason FROM flows` + where + ` ORDER BY ` + flowSortColumns[p.SortBy] + ` ` + p.SortOrder + `, id ` + p.SortOrder + ` LIMIT ? OFFSET ?`
	rows, err := s.readDB.Query(query, append(args, p.Limit, p.Offset)...)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()
	records, err := scanFlowRecords(rows)
	return QueryResult{Total: total, Records: records}, err
}

func (s *Store) QueryRejections(params RejectionQueryParams) (RejectionQueryResult, error) {
	p, err := normalizeRejectionQuery(params)
	if err != nil {
		return RejectionQueryResult{}, err
	}
	where, args, err := rejectionWhere(p)
	if err != nil {
		return RejectionQueryResult{}, err
	}
	var total int
	if err := s.readDB.QueryRow(`SELECT COUNT(*) FROM rejection_events`+where, args...).Scan(&total); err != nil {
		return RejectionQueryResult{}, err
	}
	query := `SELECT id, event_id, client_ip, asn, as_org, country, protocol, port, reason, matched_rule_type, matched_rule_value, recorded_at FROM rejection_events` + where + ` ORDER BY ` + rejectionSortColumns[p.SortBy] + ` ` + p.SortOrder + `, id ` + p.SortOrder + ` LIMIT ? OFFSET ?`
	rows, err := s.readDB.Query(query, append(args, p.Limit, p.Offset)...)
	if err != nil {
		return RejectionQueryResult{}, err
	}
	defer rows.Close()
	records, err := scanRejectionRecords(rows)
	return RejectionQueryResult{Total: total, Records: records}, err
}

func (s *Store) QueryLogEvents(params LogEventQueryParams) (LogEventQueryResult, error) {
	p, err := normalizeLogEventQuery(params)
	if err != nil {
		return LogEventQueryResult{}, err
	}
	flowSQL, flowArgs, err := eventFlowSQL(p)
	if err != nil {
		return LogEventQueryResult{}, err
	}
	rejectionSQL, rejectionArgs, err := eventRejectionSQL(p)
	if err != nil {
		return LogEventQueryResult{}, err
	}
	parts := make([]string, 0, 2)
	args := make([]any, 0)
	if p.EntryType == EntryTypeAll || p.EntryType == EntryTypeFlow {
		parts = append(parts, flowSQL)
		args = append(args, flowArgs...)
	}
	if p.EntryType == EntryTypeAll || p.EntryType == EntryTypeRejection {
		parts = append(parts, rejectionSQL)
		args = append(args, rejectionArgs...)
	}
	union := strings.Join(parts, " UNION ALL ")
	var total int
	if err := s.readDB.QueryRow(`SELECT COUNT(*) FROM (`+union+`) events`, args...).Scan(&total); err != nil {
		return LogEventQueryResult{}, err
	}
	order := eventSortColumn(p.SortBy)
	query := `SELECT entry_type, ip, asn, as_org, country, protocol, port, recorded_at, upstream, bytes_up, bytes_down, duration_ms, reason, matched_rule_type, matched_rule_value, flow_id, listener, route, close_reason, source_id FROM (` + union + `) events ORDER BY ` + order + ` ` + p.SortOrder + `, source_id ` + p.SortOrder + ` LIMIT ? OFFSET ?`
	rows, err := s.readDB.Query(query, append(args, p.Limit, p.Offset)...)
	if err != nil {
		return LogEventQueryResult{}, err
	}
	defer rows.Close()
	records, err := scanLogEvents(rows)
	return LogEventQueryResult{Total: total, Records: records}, err
}

func (s *Store) GetTopTalkers(params TopTalkerParams) ([]TopTalker, error) {
	if params.Limit <= 0 {
		params.Limit = 10
	}
	if params.Limit > MaxQueryLimit {
		params.Limit = MaxQueryLimit
	}
	where := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if params.StartTime != nil {
		where = append(where, "ended_at >= ?")
		args = append(args, *params.StartTime*1000)
	}
	if params.EndTime != nil {
		where = append(where, "ended_at <= ?")
		args = append(args, *params.EndTime*1000)
	}
	if strings.TrimSpace(params.Protocol) != "" {
		where = append(where, "protocol = ?")
		args = append(args, strings.ToLower(strings.TrimSpace(params.Protocol)))
	}
	if strings.TrimSpace(params.Upstream) != "" {
		where = append(where, "upstream = ?")
		args = append(args, strings.TrimSpace(params.Upstream))
	}
	query := `SELECT client_ip, COALESCE(SUM(bytes_up),0), COALESCE(SUM(bytes_down),0), COALESCE(SUM(bytes_up + bytes_down),0), COUNT(*) FROM flows`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " GROUP BY client_ip ORDER BY (SUM(bytes_up) + SUM(bytes_down)) DESC, client_ip ASC LIMIT ?"
	args = append(args, params.Limit)
	rows, err := s.readDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TopTalker, 0)
	for rows.Next() {
		var item TopTalker
		if err := rows.Scan(&item.ClientIP, &item.BytesUp, &item.BytesDown, &item.BytesTotal, &item.FlowCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func NormalizeQueryParams(params QueryParams) (QueryParams, error) {
	return normalizeQuery(params)
}

func NormalizeRejectionQueryParams(params RejectionQueryParams) (RejectionQueryParams, error) {
	return normalizeRejectionQuery(params)
}

func NormalizeLogEventQueryParams(params LogEventQueryParams) (LogEventQueryParams, error) {
	return normalizeLogEventQuery(params)
}

func (s *Store) AppendFlowTagEvent(event FlowTagEvent) error {
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := s.writeDB.Exec(`INSERT OR REPLACE INTO flow_tag_events(event_id, flow_id, tag, operation, source, actor, expires_at, created_at, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.EventID, event.FlowID, event.Tag, event.Operation, event.Source, event.Actor, nullableTime(event.ExpiresAt), unixMilli(event.CreatedAt), event.Metadata)
	return err
}

func (s *Store) UpsertFlowTag(tag FlowTag) error {
	now := time.Now().UTC()
	if tag.CreatedAt.IsZero() {
		tag.CreatedAt = now
	}
	if tag.UpdatedAt.IsZero() {
		tag.UpdatedAt = now
	}
	_, err := s.writeDB.Exec(`INSERT INTO flow_tags(flow_id, tag, source, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(flow_id, tag) DO UPDATE SET source=excluded.source, expires_at=excluded.expires_at, updated_at=excluded.updated_at`, tag.FlowID, tag.Tag, tag.Source, nullableTime(tag.ExpiresAt), unixMilli(tag.CreatedAt), unixMilli(tag.UpdatedAt))
	return err
}

func (s *Store) UpsertClientTag(tag ClientTag) error {
	now := time.Now().UTC()
	if tag.CreatedAt.IsZero() {
		tag.CreatedAt = now
	}
	if tag.UpdatedAt.IsZero() {
		tag.UpdatedAt = now
	}
	_, err := s.writeDB.Exec(`INSERT INTO client_tags(client_ip, tag, source, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(client_ip, tag) DO UPDATE SET source=excluded.source, expires_at=excluded.expires_at, updated_at=excluded.updated_at`, tag.ClientIP, tag.Tag, tag.Source, nullableTime(tag.ExpiresAt), unixMilli(tag.CreatedAt), unixMilli(tag.UpdatedAt))
	return err
}

func (s *Store) UpsertOnlineRule(rule OnlineRule) error {
	now := time.Now().UTC()
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = now
	}
	_, err := s.writeDB.Exec(`INSERT INTO online_rules(rule_id, version, action, rule_type, rule_value, protocol, port, enabled, expires_at, source, payload_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(rule_id) DO UPDATE SET version=excluded.version, action=excluded.action, rule_type=excluded.rule_type, rule_value=excluded.rule_value, protocol=excluded.protocol, port=excluded.port, enabled=excluded.enabled, expires_at=excluded.expires_at, source=excluded.source, payload_json=excluded.payload_json, updated_at=excluded.updated_at`, rule.RuleID, rule.Version, rule.Action, rule.RuleType, rule.RuleValue, rule.Protocol, nullableInt(rule.Port), boolInt(rule.Enabled), nullableTime(rule.ExpiresAt), rule.Source, rule.PayloadJSON, unixMilli(rule.CreatedAt), unixMilli(rule.UpdatedAt))
	return err
}

func (s *Store) RecordPolicyEvent(event PolicyEvent) error {
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	_, err := s.writeDB.Exec(`INSERT OR REPLACE INTO policy_events(event_id, flow_id, client_ip, policy_version, rule_id, decision, rule_type, rule_value, reason, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.EventID, nullIfEmpty(event.FlowID), event.ClientIP, event.PolicyVersion, event.RuleID, event.Decision, event.RuleType, event.RuleValue, event.Reason, unixMilli(event.OccurredAt))
	return err
}

func (s *Store) QueryFlowTags(flowID string) ([]FlowTag, error) {
	rows, err := s.readDB.Query(`SELECT flow_id, tag, source, expires_at, created_at, updated_at FROM flow_tags WHERE flow_id = ? AND (expires_at IS NULL OR expires_at > ?) ORDER BY tag`, flowID, time.Now().UTC().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]FlowTag, 0)
	for rows.Next() {
		var tag FlowTag
		var expires, created, updated sql.NullInt64
		if err := rows.Scan(&tag.FlowID, &tag.Tag, &tag.Source, &expires, &created, &updated); err != nil {
			return nil, err
		}
		tag.ExpiresAt = timeFromNullable(expires)
		tag.CreatedAt = timeFromMillis(created.Int64)
		tag.UpdatedAt = timeFromMillis(updated.Int64)
		result = append(result, tag)
	}
	return result, rows.Err()
}

func (s *Store) QueryClientTags(clientIP string) ([]ClientTag, error) {
	rows, err := s.readDB.Query(`SELECT client_ip, tag, source, expires_at, created_at, updated_at FROM client_tags WHERE client_ip = ? AND (expires_at IS NULL OR expires_at > ?) ORDER BY tag`, clientIP, time.Now().UTC().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ClientTag, 0)
	for rows.Next() {
		var tag ClientTag
		var expires, created, updated sql.NullInt64
		if err := rows.Scan(&tag.ClientIP, &tag.Tag, &tag.Source, &expires, &created, &updated); err != nil {
			return nil, err
		}
		tag.ExpiresAt = timeFromNullable(expires)
		tag.CreatedAt = timeFromMillis(created.Int64)
		tag.UpdatedAt = timeFromMillis(updated.Int64)
		result = append(result, tag)
	}
	return result, rows.Err()
}

func (s *Store) QueryPolicyEvents(flowID string) ([]PolicyEvent, error) {
	rows, err := s.readDB.Query(`SELECT event_id, flow_id, client_ip, policy_version, rule_id, decision, rule_type, rule_value, reason, occurred_at FROM policy_events WHERE flow_id = ? ORDER BY occurred_at, id`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PolicyEvent, 0)
	for rows.Next() {
		var event PolicyEvent
		var flowIDValue sql.NullString
		var occurred int64
		if err := rows.Scan(&event.EventID, &flowIDValue, &event.ClientIP, &event.PolicyVersion, &event.RuleID, &event.Decision, &event.RuleType, &event.RuleValue, &event.Reason, &occurred); err != nil {
			return nil, err
		}
		event.FlowID = flowIDValue.String
		event.OccurredAt = timeFromMillis(occurred)
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) Close() error {
	var errs []error
	if s != nil && s.writeDB != nil {
		errs = append(errs, s.writeDB.Close())
	}
	if s != nil && s.readDB != nil {
		errs = append(errs, s.readDB.Close())
	}
	return errors.Join(errs...)
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

func flowRecordFromClose(record EnrichedRecord) FlowRecord {
	event := record.CloseEvent
	ended := event.EndedAt
	if ended.IsZero() {
		ended = event.RecordedAt
	}
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	started := event.StartedAt
	if started.IsZero() {
		started = ended.Add(-time.Duration(event.DurationMs) * time.Millisecond)
	}
	last := event.LastActivity
	if last.IsZero() {
		last = ended
	}
	listener := event.Listener
	if listener == "" && event.Port > 0 {
		listener = fmt.Sprintf(":%d", event.Port)
	}
	return FlowRecord{FlowID: event.FlowID, Protocol: event.Protocol, ClientIP: event.IP, ClientPort: event.ClientPort, Listener: listener, Route: event.Route, Upstream: event.Upstream, StartedAt: started, EndedAt: ended, LastActivity: last, BytesUp: event.BytesUp, BytesDown: event.BytesDown, CloseReason: event.CloseReason, Fingerprint: event.Fingerprint, PolicyVersion: event.PolicyVersion, RuleID: event.RuleID, ASN: record.ASN, ASOrg: record.ASOrg, Country: record.Country}
}

func normalizeFlowTimes(record FlowRecord) (time.Time, time.Time, time.Time) {
	ended := defaultTime(record.EndedAt)
	started := defaultTime(record.StartedAt)
	if started.After(ended) {
		started = ended
	}
	last := defaultTime(record.LastActivity)
	return started, ended, last
}

func defaultTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func unixMilli(value time.Time) int64 { return defaultTime(value).UnixMilli() }

func optionalIPBytes(value string) ([]byte, int) {
	bytes, family, err := ipBytes(value)
	if err != nil {
		return []byte{}, 0
	}
	return bytes, family
}

func nullInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return unixMilli(*value)
}

func timeFromNullable(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := timeFromMillis(value.Int64)
	return &result
}

func timeFromMillis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func minPositive(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 || a < b {
		return a
	}
	return b
}

func maxPositive(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func normalizeQuery(params QueryParams) (QueryParams, error) {
	out := params
	if out.CIDR != "" && out.StartTime == nil && out.EndTime == nil {
		return QueryParams{}, errors.New("cidr requires start_time or end_time")
	}
	if out.Limit <= 0 {
		out.Limit = DefaultQueryLimit
	}
	if out.Limit > MaxQueryLimit {
		out.Limit = MaxQueryLimit
	}
	if out.Offset < 0 {
		return QueryParams{}, errors.New("offset must be >= 0")
	}
	if out.StartTime != nil && out.EndTime != nil && *out.StartTime > *out.EndTime {
		return QueryParams{}, errors.New("start_time must be <= end_time")
	}
	out.Country = strings.ToUpper(strings.TrimSpace(out.Country))
	if out.SortBy == "" {
		out.SortBy = "recorded_at"
	}
	if _, ok := flowSortColumns[out.SortBy]; !ok {
		return QueryParams{}, errors.New("invalid sort_by: must be one of recorded_at, bytes_up, bytes_down, bytes_total, duration_ms")
	}
	out.SortOrder = strings.ToLower(strings.TrimSpace(out.SortOrder))
	if out.SortOrder == "" {
		out.SortOrder = "desc"
	}
	if out.SortOrder != "asc" && out.SortOrder != "desc" {
		return QueryParams{}, errors.New("invalid sort_order: must be asc or desc")
	}
	return out, nil
}

func normalizeRejectionQuery(params RejectionQueryParams) (RejectionQueryParams, error) {
	out := params
	if out.CIDR != "" && out.StartTime == nil && out.EndTime == nil {
		return RejectionQueryParams{}, errors.New("cidr requires start_time or end_time")
	}
	if out.Limit <= 0 {
		out.Limit = DefaultQueryLimit
	}
	if out.Limit > MaxQueryLimit {
		out.Limit = MaxQueryLimit
	}
	if out.Offset < 0 {
		return RejectionQueryParams{}, errors.New("offset must be >= 0")
	}
	if out.StartTime != nil && out.EndTime != nil && *out.StartTime > *out.EndTime {
		return RejectionQueryParams{}, errors.New("start_time must be <= end_time")
	}
	out.Country = strings.ToUpper(strings.TrimSpace(out.Country))
	out.Protocol = strings.ToLower(strings.TrimSpace(out.Protocol))
	if out.Protocol != "" && out.Protocol != "tcp" && out.Protocol != "udp" {
		return RejectionQueryParams{}, errors.New("protocol must be tcp or udp")
	}
	if out.Port != nil && *out.Port <= 0 {
		return RejectionQueryParams{}, errors.New("port must be > 0")
	}
	if out.SortBy == "" {
		out.SortBy = "recorded_at"
	}
	if _, ok := rejectionSortColumns[out.SortBy]; !ok {
		return RejectionQueryParams{}, errors.New("invalid sort_by")
	}
	out.SortOrder = strings.ToLower(strings.TrimSpace(out.SortOrder))
	if out.SortOrder == "" {
		out.SortOrder = "desc"
	}
	if out.SortOrder != "asc" && out.SortOrder != "desc" {
		return RejectionQueryParams{}, errors.New("invalid sort_order: must be asc or desc")
	}
	return out, nil
}

func normalizeLogEventQuery(params LogEventQueryParams) (LogEventQueryParams, error) {
	params.EntryType = strings.ToLower(strings.TrimSpace(params.EntryType))
	if params.EntryType == "" {
		params.EntryType = EntryTypeAll
	}
	if params.EntryType != EntryTypeAll && params.EntryType != EntryTypeFlow && params.EntryType != EntryTypeRejection {
		return LogEventQueryParams{}, errors.New("entry_type must be all, flow, or rejection")
	}
	if params.Limit <= 0 {
		params.Limit = DefaultQueryLimit
	}
	if params.Limit > MaxQueryLimit {
		params.Limit = MaxQueryLimit
	}
	if params.Offset < 0 {
		return LogEventQueryParams{}, errors.New("offset must be >= 0")
	}
	if params.StartTime != nil && params.EndTime != nil && *params.StartTime > *params.EndTime {
		return LogEventQueryParams{}, errors.New("start_time must be <= end_time")
	}
	params.Protocol = strings.ToLower(strings.TrimSpace(params.Protocol))
	if params.Protocol != "" && params.Protocol != "tcp" && params.Protocol != "udp" {
		return LogEventQueryParams{}, errors.New("protocol must be tcp or udp")
	}
	if params.Port != nil && *params.Port <= 0 {
		return LogEventQueryParams{}, errors.New("port must be > 0")
	}
	params.Country = strings.ToUpper(strings.TrimSpace(params.Country))
	params.SortOrder = strings.ToLower(strings.TrimSpace(params.SortOrder))
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}
	if params.SortOrder != "asc" && params.SortOrder != "desc" {
		return LogEventQueryParams{}, errors.New("invalid sort_order: must be asc or desc")
	}
	if params.SortBy == "" {
		params.SortBy = "recorded_at"
	}
	allowed := map[string]bool{"recorded_at": true, "ip": true, "asn": true, "country": true, "protocol": true, "port": true, "entry_type": true}
	if params.EntryType == EntryTypeFlow {
		allowed["upstream"], allowed["bytes_up"], allowed["bytes_down"], allowed["bytes_total"], allowed["duration_ms"] = true, true, true, true, true
	}
	if params.EntryType == EntryTypeRejection {
		allowed["reason"], allowed["matched_rule_type"], allowed["matched_rule_value"] = true, true, true
	}
	if !allowed[params.SortBy] {
		return LogEventQueryParams{}, errors.New("invalid sort_by")
	}
	return params, nil
}

func flowWhere(params QueryParams) (string, []any, error) {
	where := make([]string, 0, 7)
	args := make([]any, 0, 7)
	if params.StartTime != nil {
		where = append(where, "ended_at >= ?")
		args = append(args, *params.StartTime*1000)
	}
	if params.EndTime != nil {
		where = append(where, "ended_at <= ?")
		args = append(args, *params.EndTime*1000)
	}
	if params.ASN != nil {
		where = append(where, "asn = ?")
		args = append(args, *params.ASN)
	}
	if params.Country != "" {
		where = append(where, "country = ?")
		args = append(args, params.Country)
	}
	if params.CIDR != "" {
		family, start, end, err := cidrRange(params.CIDR)
		if err != nil {
			return "", nil, err
		}
		where = append(where, "client_ip_family = ? AND client_ip_bytes >= ? AND client_ip_bytes <= ?")
		args = append(args, family, start, end)
	}
	if len(where) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(where, " AND "), args, nil
}

func rejectionWhere(params RejectionQueryParams) (string, []any, error) {
	where := make([]string, 0, 10)
	args := make([]any, 0, 10)
	if params.StartTime != nil {
		where = append(where, "recorded_at >= ?")
		args = append(args, *params.StartTime*1000)
	}
	if params.EndTime != nil {
		where = append(where, "recorded_at <= ?")
		args = append(args, *params.EndTime*1000)
	}
	if params.ASN != nil {
		where = append(where, "asn = ?")
		args = append(args, *params.ASN)
	}
	if params.Country != "" {
		where = append(where, "country = ?")
		args = append(args, params.Country)
	}
	if params.CIDR != "" {
		family, start, end, err := cidrRange(params.CIDR)
		if err != nil {
			return "", nil, err
		}
		where = append(where, "client_ip_family = ? AND client_ip_bytes >= ? AND client_ip_bytes <= ?")
		args = append(args, family, start, end)
	}
	if params.Reason != "" {
		where = append(where, "reason = ?")
		args = append(args, params.Reason)
	}
	if params.Protocol != "" {
		where = append(where, "protocol = ?")
		args = append(args, params.Protocol)
	}
	if params.Port != nil {
		where = append(where, "port = ?")
		args = append(args, *params.Port)
	}
	if params.MatchedRuleType != "" {
		where = append(where, "matched_rule_type = ?")
		args = append(args, params.MatchedRuleType)
	}
	if params.MatchedRuleValue != "" {
		where = append(where, "matched_rule_value = ?")
		args = append(args, params.MatchedRuleValue)
	}
	if len(where) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(where, " AND "), args, nil
}

func cidrRange(raw string) (int, []byte, []byte, error) {
	_, network, err := net.ParseCIDR(raw)
	if err != nil {
		return 0, nil, nil, err
	}
	if start := network.IP.To4(); start != nil {
		end := make(net.IP, 4)
		copy(end, start)
		for i := range end {
			end[i] |= ^network.Mask[i]
		}
		return 4, []byte(start), []byte(end), nil
	}
	start := network.IP.To16()
	end := make(net.IP, 16)
	copy(end, start)
	for i := range end {
		end[i] |= ^network.Mask[i]
	}
	return 6, []byte(start), []byte(end), nil
}

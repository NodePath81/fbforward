package audit

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"time"
)

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
	stmt, err := tx.Prepare(`INSERT INTO flows(flow_id, protocol, client_ip, client_port, client_ip_bytes, client_ip_family, listener, route, upstream, started_at, ended_at, last_activity_at, bytes_up, bytes_down, close_reason, fingerprint, policy_version, rule_id, asn, as_org, country) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(flow_id) DO UPDATE SET protocol=excluded.protocol, client_ip=excluded.client_ip, client_port=excluded.client_port, client_ip_bytes=excluded.client_ip_bytes, client_ip_family=excluded.client_ip_family, listener=excluded.listener, route=excluded.route, upstream=excluded.upstream, started_at=excluded.started_at, ended_at=excluded.ended_at, last_activity_at=excluded.last_activity_at, bytes_up=excluded.bytes_up, bytes_down=excluded.bytes_down, close_reason=excluded.close_reason, fingerprint=excluded.fingerprint, policy_version=excluded.policy_version, rule_id=excluded.rule_id, asn=excluded.asn, as_org=excluded.as_org, country=excluded.country`)
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
		endedCopy := ended
		if err := upsertFlowEntityTx(tx, FlowEntity{
			FlowID: record.FlowID, Protocol: record.Protocol, ClientIP: record.ClientIP, ClientPort: record.ClientPort,
			Listener: record.Listener, Route: record.Route, Upstream: record.Upstream, CreatedAt: started,
			EndedAt: &endedCopy, State: "closed", LastActivity: last,
			BytesUp: record.BytesUp, BytesDown: record.BytesDown,
		}); err != nil {
			_ = tx.Rollback()
			return err
		}
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

func (s *Store) InsertFlowEntities(records []FlowEntity) error {
	if s == nil || len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := upsertFlowEntityTx(tx, record); err != nil {
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

func (s *Store) UpsertFlowEntity(entity FlowEntity) error {
	if s == nil || strings.TrimSpace(entity.FlowID) == "" {
		return errors.New("flow entity is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	if err := upsertFlowEntityTx(tx, entity); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func upsertFlowEntityTx(tx *sql.Tx, entity FlowEntity) error {
	if strings.TrimSpace(entity.FlowID) == "" {
		return errors.New("flow entity is required")
	}
	if entity.Protocol == "" {
		entity.Protocol = "unknown"
	}
	if entity.State == "" {
		entity.State = "active"
	}
	created := entity.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	last := entity.LastActivity
	if last.IsZero() {
		last = created
	}
	_, err := tx.Exec(`INSERT INTO flow_entities(flow_id, protocol, client_ip, client_port, listener, route, upstream, backend_key, backend_protocol, backend_local, backend_remote, created_at, ended_at, resolve_until, state, generation, last_activity_at, bytes_up, bytes_down) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(flow_id) DO UPDATE SET protocol=excluded.protocol, client_ip=excluded.client_ip, client_port=excluded.client_port, listener=excluded.listener, route=excluded.route, upstream=excluded.upstream, backend_key=CASE WHEN flow_entities.state <> 'closed' AND excluded.generation >= flow_entities.generation AND excluded.backend_key <> '' THEN excluded.backend_key ELSE flow_entities.backend_key END, backend_protocol=CASE WHEN flow_entities.state <> 'closed' AND excluded.generation >= flow_entities.generation AND excluded.backend_protocol <> '' THEN excluded.backend_protocol ELSE flow_entities.backend_protocol END, backend_local=CASE WHEN flow_entities.state <> 'closed' AND excluded.generation >= flow_entities.generation AND excluded.backend_local <> '' THEN excluded.backend_local ELSE flow_entities.backend_local END, backend_remote=CASE WHEN flow_entities.state <> 'closed' AND excluded.generation >= flow_entities.generation AND excluded.backend_remote <> '' THEN excluded.backend_remote ELSE flow_entities.backend_remote END, created_at=MIN(flow_entities.created_at, excluded.created_at), ended_at=CASE WHEN excluded.ended_at IS NOT NULL THEN excluded.ended_at ELSE flow_entities.ended_at END, resolve_until=CASE WHEN excluded.resolve_until IS NOT NULL THEN excluded.resolve_until ELSE flow_entities.resolve_until END, state=CASE WHEN flow_entities.state = 'closed' AND excluded.state <> 'closed' THEN flow_entities.state ELSE excluded.state END, generation=MAX(flow_entities.generation, excluded.generation), last_activity_at=MAX(flow_entities.last_activity_at, excluded.last_activity_at), bytes_up=MAX(flow_entities.bytes_up, excluded.bytes_up), bytes_down=MAX(flow_entities.bytes_down, excluded.bytes_down)`, entity.FlowID, entity.Protocol, entity.ClientIP, entity.ClientPort, entity.Listener, entity.Route, entity.Upstream, entity.BackendKey, entity.BackendProtocol, entity.BackendLocal, entity.BackendRemote, unixMilli(created), nullableTime(entity.EndedAt), nullableTime(entity.ResolveUntil), entity.State, entity.Generation, unixMilli(last), entity.BytesUp, entity.BytesDown)
	return err
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

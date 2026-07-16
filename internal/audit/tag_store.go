package audit

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxTagViewLimit = 1000

func normalizeTagViewParams(query, scope string, limit, offset int) (string, string, int, int, error) {
	query = strings.TrimSpace(query)
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "flow" && scope != "client" {
		return "", "", 0, 0, fmt.Errorf("invalid tag scope")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > maxTagViewLimit {
		return "", "", 0, 0, fmt.Errorf("tag view limit exceeds %d", maxTagViewLimit)
	}
	if offset < 0 {
		return "", "", 0, 0, errors.New("tag view offset must be >= 0")
	}
	return query, scope, limit, offset, nil
}

// QueryEffectiveTags returns the current, unexpired Flow and Client tag
// projections for a batch of active flows. A client tag is expanded to every
// matching flow and duplicate tag values are removed per Flow.
func (s *Store) QueryEffectiveTags(flows []FlowTagLookup) (map[string][]EffectiveTag, error) {
	result := make(map[string][]EffectiveTag, len(flows))
	if s == nil || len(flows) == 0 {
		return result, nil
	}
	flowIDs := make([]string, 0, len(flows))
	clientIPs := make([]string, 0, len(flows))
	flowsByIP := make(map[string][]string, len(flows))
	seenFlow, seenIP := make(map[string]struct{}), make(map[string]struct{})
	for _, flow := range flows {
		if flow.FlowID != "" {
			result[flow.FlowID] = nil
			if _, ok := seenFlow[flow.FlowID]; !ok {
				seenFlow[flow.FlowID] = struct{}{}
				flowIDs = append(flowIDs, flow.FlowID)
			}
		}
		if flow.ClientIP != "" {
			if flow.FlowID != "" {
				flowsByIP[flow.ClientIP] = append(flowsByIP[flow.ClientIP], flow.FlowID)
			}
			if _, ok := seenIP[flow.ClientIP]; !ok {
				seenIP[flow.ClientIP] = struct{}{}
				clientIPs = append(clientIPs, flow.ClientIP)
			}
		}
	}
	now := time.Now().UTC().UnixMilli()
	type tagKey struct{ flowID, tag, scope string }
	seen := make(map[tagKey]struct{})
	appendTag := func(flowID string, tag EffectiveTag) {
		key := tagKey{flowID: flowID, tag: tag.Tag, scope: tag.Scope}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		result[flowID] = append(result[flowID], tag)
	}
	if len(flowIDs) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(flowIDs)), ",")
		rows, err := s.readDB.Query(`SELECT flow_id, tag, source, expires_at, updated_at FROM flow_tags WHERE flow_id IN (`+placeholders+`) AND (expires_at IS NULL OR expires_at > ?) ORDER BY tag`, append(stringArgs(flowIDs), now)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var flowID, tag, source string
			var expires, updated sql.NullInt64
			if err := rows.Scan(&flowID, &tag, &source, &expires, &updated); err != nil {
				rows.Close()
				return nil, err
			}
			appendTag(flowID, EffectiveTag{Tag: tag, Scope: "flow", Source: source, UpdatedAt: timeFromMillis(updated.Int64), ExpiresAt: timeFromNullable(expires)})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if len(clientIPs) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(clientIPs)), ",")
		rows, err := s.readDB.Query(`SELECT client_ip, tag, source, expires_at, updated_at FROM client_tags WHERE client_ip IN (`+placeholders+`) AND (expires_at IS NULL OR expires_at > ?) ORDER BY tag`, append(stringArgs(clientIPs), now)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var clientIP, tag, source string
			var expires, updated sql.NullInt64
			if err := rows.Scan(&clientIP, &tag, &source, &expires, &updated); err != nil {
				rows.Close()
				return nil, err
			}
			for _, flowID := range flowsByIP[clientIP] {
				appendTag(flowID, EffectiveTag{Tag: tag, Scope: "client", Source: source, UpdatedAt: timeFromMillis(updated.Int64), ExpiresAt: timeFromNullable(expires)})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

// QueryCurrentTags lists unique current tag projections for the Context page.
func (s *Store) QueryCurrentTags(query, scope string, limit, offset int) ([]EffectiveTag, bool, error) {
	query, scope, limit, offset, err := normalizeTagViewParams(query, scope, limit, offset)
	if err != nil {
		return nil, false, err
	}
	if s == nil {
		return nil, false, errors.New("audit store is nil")
	}
	now := time.Now().UTC().UnixMilli()
	parts := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if scope == "all" || scope == "flow" {
		parts = append(parts, `SELECT tag, 'flow' AS scope, source, updated_at, expires_at FROM flow_tags WHERE (expires_at IS NULL OR expires_at > ?)`)
		args = append(args, now)
	}
	if scope == "all" || scope == "client" {
		parts = append(parts, `SELECT tag, 'client' AS scope, source, updated_at, expires_at FROM client_tags WHERE (expires_at IS NULL OR expires_at > ?)`)
		args = append(args, now)
	}
	where := ""
	if query != "" {
		where = " WHERE instr(LOWER(tag), LOWER(?)) > 0 OR instr(LOWER(source), LOWER(?)) > 0"
		args = append(args, query, query)
	}
	querySQL := `SELECT tag, scope, MAX(source), MAX(updated_at), CASE WHEN SUM(CASE WHEN expires_at IS NULL THEN 1 ELSE 0 END) > 0 THEN NULL ELSE MAX(expires_at) END FROM (` + strings.Join(parts, " UNION ALL ") + `) current_tags` + where + ` GROUP BY tag, scope ORDER BY tag, scope LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, err := s.readDB.Query(querySQL, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]EffectiveTag, 0, limit)
	for rows.Next() {
		var tag, tagScope, source string
		var updated, expires sql.NullInt64
		if err := rows.Scan(&tag, &tagScope, &source, &updated, &expires); err != nil {
			return nil, false, err
		}
		result = append(result, EffectiveTag{Tag: tag, Scope: tagScope, Source: source, UpdatedAt: timeFromMillis(updated.Int64), ExpiresAt: timeFromNullable(expires)})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, nil
}

// QueryTagActions returns the most recent tag events, with the resolved
// client address joined from the flow entity when available.
func (s *Store) QueryTagActions(query string, limit, offset int) ([]FlowTagAction, bool, error) {
	query, _, limit, offset, err := normalizeTagViewParams(query, "all", limit, offset)
	if err != nil {
		return nil, false, err
	}
	if s == nil {
		return nil, false, errors.New("audit store is nil")
	}
	where, args := "", []any{}
	if query != "" {
		where = " WHERE instr(LOWER(e.tag), LOWER(?)) > 0 OR instr(LOWER(e.actor), LOWER(?)) > 0 OR instr(LOWER(e.operation), LOWER(?)) > 0"
		args = append(args, query, query, query)
	}
	args = append(args, limit+1, offset)
	rows, err := s.readDB.Query(`SELECT e.event_id, e.flow_id, e.tag, e.operation, e.source, e.actor, e.expires_at, e.created_at, e.metadata, COALESCE(fe.client_ip, '') FROM flow_tag_events e LEFT JOIN flow_entities fe ON fe.flow_id = e.flow_id`+where+` ORDER BY e.created_at DESC, e.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]FlowTagAction, 0, limit)
	for rows.Next() {
		var action FlowTagAction
		var expires, created sql.NullInt64
		if err := rows.Scan(&action.EventID, &action.FlowID, &action.Tag, &action.Operation, &action.Source, &action.Actor, &expires, &created, &action.Metadata, &action.ClientIP); err != nil {
			return nil, false, err
		}
		action.ExpiresAt = timeFromNullable(expires)
		action.CreatedAt = timeFromMillis(created.Int64)
		result = append(result, action)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, nil
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

// ApplyFlowTag atomically records the audit event and updates the current
// projection. prefix identifies one namespace/key pair; setting a tag first
// removes an older value for the same pair so labels have replacement
// semantics rather than accumulating stale values.
func (s *Store) ApplyFlowTag(entity FlowEntity, event FlowTagEvent, tag *FlowTag, prefix string) error {
	if s == nil {
		return errors.New("audit store is nil")
	}
	if event.FlowID == "" || prefix == "" {
		return errors.New("flow tag event and prefix are required")
	}
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if err := upsertFlowEntityTx(tx, entity); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO flow_tag_events(event_id, flow_id, tag, operation, source, actor, expires_at, created_at, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.EventID, event.FlowID, event.Tag, event.Operation, event.Source, event.Actor, nullableTime(event.ExpiresAt), unixMilli(event.CreatedAt), event.Metadata); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id = ? AND tag LIKE ?`, event.FlowID, prefix+"%"); err != nil {
		return fail(err)
	}
	if tag != nil {
		now := time.Now().UTC()
		if tag.CreatedAt.IsZero() {
			tag.CreatedAt = now
		}
		if tag.UpdatedAt.IsZero() {
			tag.UpdatedAt = now
		}
		if _, err := tx.Exec(`INSERT INTO flow_tags(flow_id, tag, source, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, tag.FlowID, tag.Tag, tag.Source, nullableTime(tag.ExpiresAt), unixMilli(tag.CreatedAt), unixMilli(tag.UpdatedAt)); err != nil {
			return fail(err)
		}
	}
	return tx.Commit()
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

// ApplyClientTag is the client-level equivalent of ApplyFlowTag. The flow ID
// on the event ties the client label to the backend operation that requested
// it, while the projection is keyed by the resolved client IP.
func (s *Store) ApplyClientTag(entity FlowEntity, event FlowTagEvent, tag *ClientTag, prefix string) error {
	if s == nil {
		return errors.New("audit store is nil")
	}
	if event.FlowID == "" || prefix == "" {
		return errors.New("client tag event and prefix are required")
	}
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if err := upsertFlowEntityTx(tx, entity); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO flow_tag_events(event_id, flow_id, tag, operation, source, actor, expires_at, created_at, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.EventID, event.FlowID, event.Tag, event.Operation, event.Source, event.Actor, nullableTime(event.ExpiresAt), unixMilli(event.CreatedAt), event.Metadata); err != nil {
		return fail(err)
	}
	if tag == nil {
		return fail(errors.New("client tag projection is required"))
	}
	if _, err := tx.Exec(`DELETE FROM client_tags WHERE client_ip = ? AND tag LIKE ?`, tag.ClientIP, prefix+"%"); err != nil {
		return fail(err)
	}
	now := time.Now().UTC()
	if tag.CreatedAt.IsZero() {
		tag.CreatedAt = now
	}
	if tag.UpdatedAt.IsZero() {
		tag.UpdatedAt = now
	}
	if _, err := tx.Exec(`INSERT INTO client_tags(client_ip, tag, source, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, tag.ClientIP, tag.Tag, tag.Source, nullableTime(tag.ExpiresAt), unixMilli(tag.CreatedAt), unixMilli(tag.UpdatedAt)); err != nil {
		return fail(err)
	}
	return tx.Commit()
}

// RemoveClientTag atomically records an unset event and removes the matching
// namespace/key projection.
func (s *Store) RemoveClientTag(entity FlowEntity, event FlowTagEvent, clientIP, prefix string) error {
	if s == nil {
		return errors.New("audit store is nil")
	}
	if event.FlowID == "" || clientIP == "" || prefix == "" {
		return errors.New("client tag removal fields are required")
	}
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if err := upsertFlowEntityTx(tx, entity); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO flow_tag_events(event_id, flow_id, tag, operation, source, actor, expires_at, created_at, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.EventID, event.FlowID, event.Tag, event.Operation, event.Source, event.Actor, nullableTime(event.ExpiresAt), unixMilli(event.CreatedAt), event.Metadata); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(`DELETE FROM client_tags WHERE client_ip = ? AND tag LIKE ?`, clientIP, prefix+"%"); err != nil {
		return fail(err)
	}
	return tx.Commit()
}

// RemoveFlowTag atomically records an unset event and removes the matching
// namespace/key projection.
func (s *Store) RemoveFlowTag(entity FlowEntity, event FlowTagEvent, prefix string) error {
	if s == nil {
		return errors.New("audit store is nil")
	}
	if event.FlowID == "" || prefix == "" {
		return errors.New("flow tag removal fields are required")
	}
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if err := upsertFlowEntityTx(tx, entity); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO flow_tag_events(event_id, flow_id, tag, operation, source, actor, expires_at, created_at, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.EventID, event.FlowID, event.Tag, event.Operation, event.Source, event.Actor, nullableTime(event.ExpiresAt), unixMilli(event.CreatedAt), event.Metadata); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id = ? AND tag LIKE ?`, event.FlowID, prefix+"%"); err != nil {
		return fail(err)
	}
	return tx.Commit()
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

func (s *Store) QueryFlowTagEvents(flowID string) ([]FlowTagEvent, error) {
	rows, err := s.readDB.Query(`SELECT event_id, flow_id, tag, operation, source, actor, expires_at, created_at, metadata FROM flow_tag_events WHERE flow_id = ? ORDER BY created_at, id`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]FlowTagEvent, 0)
	for rows.Next() {
		var event FlowTagEvent
		var expires, created sql.NullInt64
		if err := rows.Scan(&event.EventID, &event.FlowID, &event.Tag, &event.Operation, &event.Source, &event.Actor, &expires, &created, &event.Metadata); err != nil {
			return nil, err
		}
		event.ExpiresAt = timeFromNullable(expires)
		event.CreatedAt = timeFromMillis(created.Int64)
		result = append(result, event)
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

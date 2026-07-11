package audit

import (
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"time"
)

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

package audit

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

func (s *Store) UpsertOnlineRule(rule OnlineRule) error {
	now := time.Now().UTC()
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = now
	}
	_, err := s.writeDB.Exec(`INSERT INTO online_rules(rule_id, version, action, rule_type, rule_value, protocol, port, priority, enabled, expires_at, source, created_by, reason, ticket_ref, matcher_json, params_json, payload_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(rule_id) DO UPDATE SET version=excluded.version, action=excluded.action, rule_type=excluded.rule_type, rule_value=excluded.rule_value, protocol=excluded.protocol, port=excluded.port, priority=excluded.priority, enabled=excluded.enabled, expires_at=excluded.expires_at, source=excluded.source, created_by=excluded.created_by, reason=excluded.reason, ticket_ref=excluded.ticket_ref, matcher_json=excluded.matcher_json, params_json=excluded.params_json, payload_json=excluded.payload_json, updated_at=excluded.updated_at`, rule.RuleID, rule.Version, rule.Action, rule.RuleType, rule.RuleValue, rule.Protocol, nullableInt(rule.Port), rule.Priority, boolInt(rule.Enabled), nullableTime(rule.ExpiresAt), rule.Source, rule.CreatedBy, rule.Reason, rule.TicketRef, rule.MatcherJSON, rule.ParamsJSON, rule.PayloadJSON, unixMilli(rule.CreatedAt), unixMilli(rule.UpdatedAt))
	return err
}

var (
	ErrOnlineRuleExists   = errors.New("online rule already exists")
	ErrOnlineRuleNotFound = errors.New("online rule not found")
)

func (s *Store) CreateOnlineRule(rule OnlineRule, event OnlineRuleEvent) error {
	if s == nil || strings.TrimSpace(rule.RuleID) == "" {
		return errors.New("online rule id is required")
	}
	if event.RuleID == "" {
		event.RuleID = rule.RuleID
	}
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	now := time.Now().UTC()
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = now
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = now
	}
	if event.PayloadJSON == "" {
		event.PayloadJSON = onlineRulePayload(rule)
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
	_, err = tx.Exec(`INSERT INTO online_rules(rule_id, version, action, rule_type, rule_value, protocol, port, priority, enabled, expires_at, source, created_by, reason, ticket_ref, matcher_json, params_json, payload_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, rule.RuleID, rule.Version, rule.Action, rule.RuleType, rule.RuleValue, rule.Protocol, nullableInt(rule.Port), rule.Priority, boolInt(rule.Enabled), nullableTime(rule.ExpiresAt), rule.Source, rule.CreatedBy, rule.Reason, rule.TicketRef, rule.MatcherJSON, rule.ParamsJSON, rule.PayloadJSON, unixMilli(rule.CreatedAt), unixMilli(rule.UpdatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: online_rules.rule_id") {
			return fail(ErrOnlineRuleExists)
		}
		return fail(err)
	}
	if err := insertOnlineRuleEventTx(tx, event, rule.Action); err != nil {
		return fail(err)
	}
	return tx.Commit()
}

func (s *Store) ListOnlineRules(now time.Time, includeExpired bool) ([]OnlineRule, error) {
	if s == nil {
		return nil, errors.New("audit store is nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := `SELECT rule_id, version, action, rule_type, rule_value, protocol, port, priority, enabled, expires_at, source, created_by, reason, ticket_ref, matcher_json, params_json, payload_json, created_at, updated_at FROM online_rules`
	args := []any{}
	if !includeExpired {
		query += ` WHERE enabled = 1 AND (expires_at IS NULL OR expires_at > ?)`
		args = append(args, unixMilli(now))
	}
	query += ` ORDER BY priority DESC, created_at ASC, rule_id ASC`
	rows, err := s.readDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]OnlineRule, 0)
	for rows.Next() {
		rule, err := scanOnlineRule(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

func (s *Store) DeleteOnlineRule(ruleID string, event OnlineRuleEvent) error {
	if s == nil || strings.TrimSpace(ruleID) == "" {
		return errors.New("online rule id is required")
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
	rule, err := selectOnlineRuleTx(tx, ruleID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fail(ErrOnlineRuleNotFound)
		}
		return fail(err)
	}
	if _, err := tx.Exec(`DELETE FROM online_rules WHERE rule_id = ?`, ruleID); err != nil {
		return fail(err)
	}
	event.RuleID = ruleID
	if event.Operation == "" {
		event.Operation = "delete"
	}
	if event.PayloadJSON == "" {
		event.PayloadJSON = onlineRulePayload(rule)
	}
	if err := insertOnlineRuleEventTx(tx, event, rule.Action); err != nil {
		return fail(err)
	}
	return tx.Commit()
}

func (s *Store) ExpireOnlineRule(ruleID string, now time.Time, event OnlineRuleEvent) error {
	if s == nil || strings.TrimSpace(ruleID) == "" {
		return errors.New("online rule id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
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
	rule, err := selectOnlineRuleTx(tx, ruleID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fail(ErrOnlineRuleNotFound)
		}
		return fail(err)
	}
	if _, err := tx.Exec(`UPDATE online_rules SET enabled = 0, expires_at = ?, updated_at = ? WHERE rule_id = ?`, unixMilli(now), unixMilli(now), ruleID); err != nil {
		return fail(err)
	}
	event.RuleID = ruleID
	if event.Operation == "" {
		event.Operation = "expire"
	}
	if event.PayloadJSON == "" {
		event.PayloadJSON = onlineRulePayload(rule)
	}
	if err := insertOnlineRuleEventTx(tx, event, rule.Action); err != nil {
		return fail(err)
	}
	return tx.Commit()
}

func (s *Store) ExpireDueOnlineRules(now time.Time) ([]OnlineRule, error) {
	if s == nil {
		return nil, errors.New("audit store is nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.writeDB.Begin()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT rule_id, version, action, rule_type, rule_value, protocol, port, priority, enabled, expires_at, source, created_by, reason, ticket_ref, matcher_json, params_json, payload_json, created_at, updated_at FROM online_rules WHERE enabled = 1 AND expires_at IS NOT NULL AND expires_at <= ?`, unixMilli(now))
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	rules := make([]OnlineRule, 0)
	for rows.Next() {
		rule, scanErr := scanOnlineRule(rows)
		if scanErr != nil {
			rows.Close()
			_ = tx.Rollback()
			return nil, scanErr
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		_ = tx.Rollback()
		return nil, err
	}
	rows.Close()
	for _, rule := range rules {
		if _, err := tx.Exec(`UPDATE online_rules SET enabled = 0, updated_at = ? WHERE rule_id = ?`, unixMilli(now), rule.RuleID); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		event := OnlineRuleEvent{RuleID: rule.RuleID, Operation: "expire", Actor: "system", Reason: "ttl expired", OccurredAt: now, PayloadJSON: onlineRulePayload(rule)}
		if err := insertOnlineRuleEventTx(tx, event, rule.Action); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rules, nil
}

func selectOnlineRuleTx(tx *sql.Tx, ruleID string) (OnlineRule, error) {
	return scanOnlineRule(tx.QueryRow(`SELECT rule_id, version, action, rule_type, rule_value, protocol, port, priority, enabled, expires_at, source, created_by, reason, ticket_ref, matcher_json, params_json, payload_json, created_at, updated_at FROM online_rules WHERE rule_id = ?`, ruleID))
}

func onlineRulePayload(rule OnlineRule) string {
	matcher := json.RawMessage("null")
	if rule.MatcherJSON != "" {
		matcher = json.RawMessage(rule.MatcherJSON)
	} else if rule.RuleType != "" && rule.RuleValue != "" {
		matcherKey := rule.RuleType
		if matcherKey == "cidr" {
			matcherKey = "source_cidr"
		} else if matcherKey == "ip" {
			matcherKey = "source_ip"
		}
		if raw, err := json.Marshal(map[string]string{matcherKey: rule.RuleValue}); err == nil {
			matcher = raw
		}
	}
	params := json.RawMessage("null")
	if rule.ParamsJSON != "" {
		params = json.RawMessage(rule.ParamsJSON)
	} else if rule.PayloadJSON != "" {
		params = json.RawMessage(rule.PayloadJSON)
	}
	if !json.Valid(matcher) {
		matcher = json.RawMessage("null")
	}
	if !json.Valid(params) {
		params = json.RawMessage("null")
	}
	payload, err := json.Marshal(map[string]any{
		"matcher":    matcher,
		"params":     params,
		"priority":   rule.Priority,
		"reason":     rule.Reason,
		"ticket_ref": rule.TicketRef,
		"created_by": rule.CreatedBy,
		"created_at": rule.CreatedAt,
		"expires_at": rule.ExpiresAt,
		"rule_id":    rule.RuleID,
		"action":     rule.Action,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}

func insertOnlineRuleEventTx(tx *sql.Tx, event OnlineRuleEvent, action string) error {
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.Operation == "" {
		event.Operation = "create"
	}
	if event.Action == "" {
		event.Action = action
	}
	_, err := tx.Exec(`INSERT INTO online_rule_events(event_id, rule_id, operation, action, actor, reason, ticket_ref, payload_json, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.EventID, event.RuleID, event.Operation, event.Action, event.Actor, event.Reason, event.TicketRef, event.PayloadJSON, unixMilli(event.OccurredAt))
	return err
}

func scanOnlineRule(scanner interface{ Scan(...any) error }) (OnlineRule, error) {
	var rule OnlineRule
	var port, expires, created, updated sql.NullInt64
	if err := scanner.Scan(&rule.RuleID, &rule.Version, &rule.Action, &rule.RuleType, &rule.RuleValue, &rule.Protocol, &port, &rule.Priority, &rule.Enabled, &expires, &rule.Source, &rule.CreatedBy, &rule.Reason, &rule.TicketRef, &rule.MatcherJSON, &rule.ParamsJSON, &rule.PayloadJSON, &created, &updated); err != nil {
		return OnlineRule{}, err
	}
	if port.Valid {
		value := int(port.Int64)
		rule.Port = &value
	}
	rule.ExpiresAt = timeFromNullable(expires)
	if created.Valid {
		rule.CreatedAt = timeFromMillis(created.Int64)
	}
	if updated.Valid {
		rule.UpdatedAt = timeFromMillis(updated.Int64)
	}
	return rule, nil
}

func (s *Store) QueryOnlineRuleEvents(ruleID string) ([]OnlineRuleEvent, error) {
	rows, err := s.readDB.Query(`SELECT event_id, rule_id, operation, action, actor, reason, ticket_ref, payload_json, occurred_at FROM online_rule_events WHERE rule_id = ? ORDER BY occurred_at, id`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]OnlineRuleEvent, 0)
	for rows.Next() {
		var event OnlineRuleEvent
		var occurred int64
		if err := rows.Scan(&event.EventID, &event.RuleID, &event.Operation, &event.Action, &event.Actor, &event.Reason, &event.TicketRef, &event.PayloadJSON, &occurred); err != nil {
			return nil, err
		}
		event.OccurredAt = timeFromMillis(occurred)
		result = append(result, event)
	}
	return result, rows.Err()
}

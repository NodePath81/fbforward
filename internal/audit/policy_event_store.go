package audit

import (
	"database/sql"
	"github.com/google/uuid"
	"time"
)

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

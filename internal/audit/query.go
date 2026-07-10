package audit

import (
	"database/sql"
	"fmt"
	"strings"
)

func eventFlowSQL(params LogEventQueryParams) (string, []any, error) {
	where, args, err := eventWhere(params, false)
	if err != nil {
		return "", nil, err
	}
	query := `SELECT 'flow' AS entry_type, client_ip AS ip, asn, as_org, country, protocol, ` + listenerPortSQL + ` AS port, ended_at AS recorded_at, upstream, bytes_up, bytes_down, (ended_at-started_at) AS duration_ms, NULL AS reason, NULL AS matched_rule_type, NULL AS matched_rule_value, flow_id, listener, route, close_reason, id AS source_id FROM flows`
	return query + where, args, nil
}

func eventRejectionSQL(params LogEventQueryParams) (string, []any, error) {
	where, args, err := eventWhere(params, true)
	if err != nil {
		return "", nil, err
	}
	query := `SELECT 'rejection' AS entry_type, client_ip AS ip, asn, as_org, country, protocol, port, recorded_at, NULL AS upstream, NULL AS bytes_up, NULL AS bytes_down, NULL AS duration_ms, reason, matched_rule_type, matched_rule_value, NULL AS flow_id, listener, NULL AS route, NULL AS close_reason, id AS source_id FROM rejection_events`
	return query + where, args, nil
}

func eventWhere(params LogEventQueryParams, rejection bool) (string, []any, error) {
	where := make([]string, 0, 10)
	args := make([]any, 0, 10)
	columnTime := "ended_at"
	if rejection {
		columnTime = "recorded_at"
	}
	if params.StartTime != nil {
		where = append(where, columnTime+" >= ?")
		args = append(args, *params.StartTime*1000)
	}
	if params.EndTime != nil {
		where = append(where, columnTime+" <= ?")
		args = append(args, *params.EndTime*1000)
	}
	if params.CIDR != "" {
		if params.StartTime == nil && params.EndTime == nil {
			return "", nil, fmt.Errorf("cidr requires start_time or end_time")
		}
		family, start, end, err := cidrRange(params.CIDR)
		if err != nil {
			return "", nil, err
		}
		where = append(where, "client_ip_family = ? AND client_ip_bytes >= ? AND client_ip_bytes <= ?")
		args = append(args, family, start, end)
	}
	if params.ASN != nil {
		where = append(where, "asn = ?")
		args = append(args, *params.ASN)
	}
	if params.Country != "" {
		where = append(where, "country = ?")
		args = append(args, params.Country)
	}
	if params.Protocol != "" {
		where = append(where, "protocol = ?")
		args = append(args, params.Protocol)
	}
	if params.Port != nil {
		where = append(where, "port = ?")
		args = append(args, *params.Port)
	}
	if rejection {
		if params.Reason != "" {
			where = append(where, "reason = ?")
			args = append(args, params.Reason)
		}
		if params.MatchedRuleType != "" {
			where = append(where, "matched_rule_type = ?")
			args = append(args, params.MatchedRuleType)
		}
		if params.MatchedRuleValue != "" {
			where = append(where, "matched_rule_value = ?")
			args = append(args, params.MatchedRuleValue)
		}
	}
	if len(where) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(where, " AND "), args, nil
}

func eventSortColumn(sortBy string) string {
	switch sortBy {
	case "ip":
		return "ip"
	case "asn":
		return "asn"
	case "country":
		return "country"
	case "protocol":
		return "protocol"
	case "port":
		return "port"
	case "entry_type":
		return "entry_type"
	case "upstream":
		return "upstream"
	case "bytes_up":
		return "bytes_up"
	case "bytes_down":
		return "bytes_down"
	case "bytes_total":
		return "(COALESCE(bytes_up, 0) + COALESCE(bytes_down, 0))"
	case "duration_ms":
		return "duration_ms"
	case "reason":
		return "reason"
	case "matched_rule_type":
		return "matched_rule_type"
	case "matched_rule_value":
		return "matched_rule_value"
	default:
		return "recorded_at"
	}
}

func scanFlowRecords(rows *sql.Rows) ([]Record, error) {
	result := make([]Record, 0)
	for rows.Next() {
		var record Record
		var asn sql.NullInt64
		var asOrg, country, flowID, listener, route, closeReason sql.NullString
		var started, ended int64
		if err := rows.Scan(&record.ID, &flowID, &record.IP, &asn, &asOrg, &country, &record.Protocol, &record.Upstream, &listener, &route, &record.Port, &record.BytesUp, &record.BytesDown, &record.DurationMs, &started, &ended, &closeReason); err != nil {
			return nil, err
		}
		record.FlowID = flowID.String
		record.ASN = int(asn.Int64)
		record.ASOrg = asOrg.String
		record.Country = country.String
		record.Listener = listener.String
		record.Route = route.String
		record.CloseReason = closeReason.String
		record.StartedAt = started
		record.EndedAt = ended
		record.RecordedAt = ended / 1000
		result = append(result, record)
	}
	return result, rows.Err()
}

func scanRejectionRecords(rows *sql.Rows) ([]RejectionRecord, error) {
	result := make([]RejectionRecord, 0)
	for rows.Next() {
		var record RejectionRecord
		var asn sql.NullInt64
		var asOrg, country, eventID, ruleType, ruleValue sql.NullString
		var recorded int64
		if err := rows.Scan(&record.ID, &eventID, &record.IP, &asn, &asOrg, &country, &record.Protocol, &record.Port, &record.Reason, &ruleType, &ruleValue, &recorded); err != nil {
			return nil, err
		}
		record.EventID = eventID.String
		record.ASN = int(asn.Int64)
		record.ASOrg = asOrg.String
		record.Country = country.String
		record.MatchedRuleType = ruleType.String
		record.MatchedRuleValue = ruleValue.String
		record.RecordedAt = recorded / 1000
		result = append(result, record)
	}
	return result, rows.Err()
}

func scanLogEvents(rows *sql.Rows) ([]LogEventRecord, error) {
	result := make([]LogEventRecord, 0)
	for rows.Next() {
		var item LogEventRecord
		var asn, port, recorded, duration, sourceID sql.NullInt64
		var asOrg, country, upstream, reason, ruleType, ruleValue, flowID, listener, route, closeReason sql.NullString
		var bytesUp, bytesDown sql.NullInt64
		if err := rows.Scan(&item.EntryType, &item.IP, &asn, &asOrg, &country, &item.Protocol, &port, &recorded, &upstream, &bytesUp, &bytesDown, &duration, &reason, &ruleType, &ruleValue, &flowID, &listener, &route, &closeReason, &sourceID); err != nil {
			return nil, err
		}
		item.ASN = int(asn.Int64)
		item.ASOrg = asOrg.String
		item.Country = country.String
		item.Port = int(port.Int64)
		item.RecordedAt = recorded.Int64 / 1000
		if upstream.Valid {
			value := upstream.String
			item.Upstream = &value
		}
		if bytesUp.Valid {
			value := uint64(bytesUp.Int64)
			item.BytesUp = &value
		}
		if bytesDown.Valid {
			value := uint64(bytesDown.Int64)
			item.BytesDown = &value
		}
		if duration.Valid {
			value := duration.Int64
			item.DurationMs = &value
		}
		if reason.Valid {
			value := reason.String
			item.Reason = &value
		}
		if ruleType.Valid {
			value := ruleType.String
			item.MatchedRuleType = &value
		}
		if ruleValue.Valid {
			value := ruleValue.String
			item.MatchedRuleValue = &value
		}
		if flowID.Valid {
			value := flowID.String
			item.FlowID = &value
		}
		if listener.Valid {
			value := listener.String
			item.Listener = &value
		}
		if route.Valid {
			value := route.String
			item.Route = &value
		}
		if closeReason.Valid {
			value := closeReason.String
			item.CloseReason = &value
		}
		item.sourceID = sourceID.Int64
		result = append(result, item)
	}
	return result, rows.Err()
}

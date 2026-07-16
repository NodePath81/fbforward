package audit

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
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
	if params.Tag != "" {
		if rejection {
			where = append(where, "1 = 0")
		} else {
			where = append(where, `(EXISTS (SELECT 1 FROM flow_tags ft WHERE ft.flow_id = flows.flow_id AND ft.tag = ? AND (ft.expires_at IS NULL OR ft.expires_at > ?)) OR EXISTS (SELECT 1 FROM client_tags ct WHERE ct.client_ip = flows.client_ip AND ct.tag = ? AND (ct.expires_at IS NULL OR ct.expires_at > ?)))`)
			now := time.Now().UTC().UnixMilli()
			args = append(args, params.Tag, now, params.Tag, now)
		}
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
	if params.IP != "" {
		where = append(where, "client_ip = ?")
		args = append(args, params.IP)
	}
	if params.Upstream != "" {
		if rejection {
			where = append(where, "1 = 0")
		} else {
			where = append(where, "upstream = ?")
			args = append(args, params.Upstream)
		}
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
	} else if params.Reason != "" || params.MatchedRuleType != "" || params.MatchedRuleValue != "" {
		where = append(where, "1 = 0")
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
	if err != nil {
		return LogEventQueryResult{}, err
	}
	if err := s.attachLogEventTags(records); err != nil {
		return LogEventQueryResult{}, err
	}
	return LogEventQueryResult{Total: total, Records: records}, err
}

func (s *Store) attachLogEventTags(records []LogEventRecord) error {
	lookups := make([]FlowTagLookup, 0, len(records))
	for _, record := range records {
		if record.EntryType != EntryTypeFlow || record.FlowID == nil || *record.FlowID == "" {
			continue
		}
		lookups = append(lookups, FlowTagLookup{FlowID: *record.FlowID, ClientIP: record.IP})
	}
	tags, err := s.QueryEffectiveTags(lookups)
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].FlowID == nil {
			continue
		}
		seen := make(map[string]struct{})
		for _, tag := range tags[*records[i].FlowID] {
			if _, ok := seen[tag.Tag]; ok {
				continue
			}
			seen[tag.Tag] = struct{}{}
			records[i].Tags = append(records[i].Tags, tag.Tag)
		}
	}
	return nil
}

func (s *Store) GetTopTalkers(params TopTalkerParams) ([]TopTalker, error) {
	if err := validateTopQuery(params.StartTime, params.EndTime, params.Protocol, params.Limit, params.Offset); err != nil {
		return nil, err
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}
	sortColumn, sortOrder, err := topSort(params.SortBy, params.SortOrder, "client_ip")
	if err != nil {
		return nil, err
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
	if strings.TrimSpace(params.Tag) != "" {
		where = append(where, `(EXISTS (SELECT 1 FROM flow_tags ft WHERE ft.flow_id = flows.flow_id AND ft.tag = ? AND (ft.expires_at IS NULL OR ft.expires_at > ?)) OR EXISTS (SELECT 1 FROM client_tags ct WHERE ct.client_ip = flows.client_ip AND ct.tag = ? AND (ct.expires_at IS NULL OR ct.expires_at > ?)))`)
		now := time.Now().UTC().UnixMilli()
		tag := strings.TrimSpace(params.Tag)
		args = append(args, tag, now, tag, now)
	}
	query := `SELECT client_ip, COALESCE(SUM(bytes_up),0), COALESCE(SUM(bytes_down),0), COALESCE(SUM(bytes_up + bytes_down),0), COUNT(*) FROM flows`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " GROUP BY client_ip ORDER BY " + sortColumn + " " + sortOrder + ", client_ip ASC LIMIT ? OFFSET ?"
	args = append(args, params.Limit, params.Offset)
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

// GetTopASNs performs the aggregation in SQLite and returns only the bounded
// result set needed by the control API.
func (s *Store) GetTopASNs(params TopASNParams) ([]TopASN, error) {
	if err := validateTopQuery(params.StartTime, params.EndTime, params.Protocol, params.Limit, params.Offset); err != nil {
		return nil, err
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}
	sortColumn, sortOrder, err := topSort(params.SortBy, params.SortOrder, "asn")
	if err != nil {
		return nil, err
	}
	where := make([]string, 0, 5)
	args := make([]any, 0, 8)
	if params.StartTime != nil {
		where = append(where, "ended_at >= ?")
		args = append(args, *params.StartTime*1000)
	}
	if params.EndTime != nil {
		where = append(where, "ended_at <= ?")
		args = append(args, *params.EndTime*1000)
	}
	if protocol := strings.ToLower(strings.TrimSpace(params.Protocol)); protocol != "" {
		where = append(where, "protocol = ?")
		args = append(args, protocol)
	}
	if upstream := strings.TrimSpace(params.Upstream); upstream != "" {
		where = append(where, "upstream = ?")
		args = append(args, upstream)
	}
	if tag := strings.TrimSpace(params.Tag); tag != "" {
		where = append(where, `(EXISTS (SELECT 1 FROM flow_tags ft WHERE ft.flow_id = flows.flow_id AND ft.tag = ? AND (ft.expires_at IS NULL OR ft.expires_at > ?)) OR EXISTS (SELECT 1 FROM client_tags ct WHERE ct.client_ip = flows.client_ip AND ct.tag = ? AND (ct.expires_at IS NULL OR ct.expires_at > ?)))`)
		now := time.Now().UTC().UnixMilli()
		args = append(args, tag, now, tag, now)
	}
	query := `SELECT COALESCE(asn, 0), COALESCE(MAX(as_org), ''), CASE WHEN COUNT(DISTINCT country) = 1 THEN COALESCE(MAX(country), '') ELSE '' END, COALESCE(SUM(bytes_up), 0), COALESCE(SUM(bytes_down), 0), COALESCE(SUM(bytes_up + bytes_down), 0), COUNT(*) FROM flows`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " GROUP BY asn ORDER BY " + sortColumn + " " + sortOrder + ", asn ASC LIMIT ? OFFSET ?"
	args = append(args, params.Limit, params.Offset)
	rows, err := s.readDB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TopASN, 0)
	for rows.Next() {
		var item TopASN
		var asn sql.NullInt64
		if err := rows.Scan(&asn, &item.ASOrg, &item.Country, &item.BytesUp, &item.BytesDown, &item.BytesTotal, &item.FlowCount); err != nil {
			return nil, err
		}
		item.ASN = int(asn.Int64)
		result = append(result, item)
	}
	return result, rows.Err()
}

// GetTopTags aggregates traffic for currently effective, unexpired tags. A
// Flow carrying the same tag through both Flow and Client projections is
// counted once for that tag.
func (s *Store) GetTopTags(params TopTagParams) ([]TopTag, error) {
	if err := validateTopQuery(params.StartTime, params.EndTime, params.Protocol, params.Limit, params.Offset); err != nil {
		return nil, err
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}
	sortColumn, sortOrder, err := topSort(params.SortBy, params.SortOrder, "tag")
	if err != nil {
		return nil, err
	}
	where := make([]string, 0, 3)
	args := make([]any, 0, 6)
	if params.StartTime != nil {
		where = append(where, "f.ended_at >= ?")
		args = append(args, *params.StartTime*1000)
	}
	if params.EndTime != nil {
		where = append(where, "f.ended_at <= ?")
		args = append(args, *params.EndTime*1000)
	}
	if protocol := strings.ToLower(strings.TrimSpace(params.Protocol)); protocol != "" {
		where = append(where, "f.protocol = ?")
		args = append(args, protocol)
	}
	if upstream := strings.TrimSpace(params.Upstream); upstream != "" {
		where = append(where, "f.upstream = ?")
		args = append(args, upstream)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " AND " + strings.Join(where, " AND ")
	}
	now := time.Now().UTC().UnixMilli()
	query := `WITH tagged AS (
		SELECT f.flow_id, ft.tag, f.bytes_up, f.bytes_down
		FROM flows f JOIN flow_tags ft ON ft.flow_id = f.flow_id
		WHERE (ft.expires_at IS NULL OR ft.expires_at > ?)` + whereSQL + `
		UNION ALL
		SELECT f.flow_id, ct.tag, f.bytes_up, f.bytes_down
		FROM flows f JOIN client_tags ct ON ct.client_ip = f.client_ip
		WHERE (ct.expires_at IS NULL OR ct.expires_at > ?)` + whereSQL + `
	), dedup AS (
		SELECT flow_id, tag, MAX(bytes_up) AS bytes_up, MAX(bytes_down) AS bytes_down
		FROM tagged GROUP BY flow_id, tag
	)
	SELECT tag, COALESCE(SUM(bytes_up), 0), COALESCE(SUM(bytes_down), 0),
	       COALESCE(SUM(bytes_up + bytes_down), 0), COUNT(*)
	FROM dedup GROUP BY tag ORDER BY ` + sortColumn + ` ` + sortOrder + `, tag ASC LIMIT ? OFFSET ?`
	queryArgs := make([]any, 0, len(args)*2+3)
	queryArgs = append(queryArgs, now)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, now)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, params.Limit, params.Offset)
	rows, err := s.readDB.Query(query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TopTag, 0, params.Limit)
	for rows.Next() {
		var item TopTag
		if err := rows.Scan(&item.Tag, &item.BytesUp, &item.BytesDown, &item.BytesTotal, &item.FlowCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func validateTopQuery(start, end *int64, protocol string, limit, offset int) error {
	if start != nil && end != nil && *start > *end {
		return errors.New("start_time must be earlier than or equal to end_time")
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "" && protocol != "tcp" && protocol != "udp" {
		return errors.New("protocol must be tcp or udp")
	}
	if limit < 0 || limit > MaxQueryLimit {
		return fmt.Errorf("limit must be between 1 and %d", MaxQueryLimit)
	}
	if offset < 0 {
		return errors.New("offset must be >= 0")
	}
	return nil
}

func topSort(sortBy, sortOrder, tieColumn string) (string, string, error) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	if sortBy == "" {
		sortBy = "bytes_total"
	}
	columns := map[string]string{
		"bytes_total": "(SUM(bytes_up) + SUM(bytes_down))",
		"bytes_up":    "SUM(bytes_up)",
		"bytes_down":  "SUM(bytes_down)",
		"flow_count":  "COUNT(*)",
	}
	if tieColumn == "client_ip" {
		columns["client_ip"] = "client_ip"
	} else if tieColumn == "asn" {
		columns["asn"] = "asn"
	} else {
		columns["tag"] = "tag"
	}
	column, ok := columns[sortBy]
	if !ok {
		return "", "", errors.New("invalid top sort_by")
	}
	sortOrder = strings.ToLower(strings.TrimSpace(sortOrder))
	if sortOrder == "" {
		sortOrder = "desc"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		return "", "", errors.New("invalid sort_order: must be asc or desc")
	}
	return column, sortOrder, nil
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
	out.Protocol = strings.ToLower(strings.TrimSpace(out.Protocol))
	if out.Protocol != "" && out.Protocol != "tcp" && out.Protocol != "udp" {
		return QueryParams{}, errors.New("protocol must be tcp or udp")
	}
	out.Tag = strings.TrimSpace(out.Tag)
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
	out.Tag = strings.TrimSpace(out.Tag)
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
	params.Tag = strings.TrimSpace(params.Tag)
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
	if params.Protocol != "" {
		where = append(where, "protocol = ?")
		args = append(args, params.Protocol)
	}
	if params.Upstream != "" {
		where = append(where, "upstream = ?")
		args = append(args, params.Upstream)
	}
	if params.IP != "" {
		where = append(where, "client_ip = ?")
		args = append(args, params.IP)
	}
	if params.CIDR != "" {
		family, start, end, err := cidrRange(params.CIDR)
		if err != nil {
			return "", nil, err
		}
		where = append(where, "client_ip_family = ? AND client_ip_bytes >= ? AND client_ip_bytes <= ?")
		args = append(args, family, start, end)
	}
	if params.Tag != "" {
		where = append(where, `(EXISTS (SELECT 1 FROM flow_tags ft WHERE ft.flow_id = flows.flow_id AND ft.tag = ? AND (ft.expires_at IS NULL OR ft.expires_at > ?)) OR EXISTS (SELECT 1 FROM client_tags ct WHERE ct.client_ip = flows.client_ip AND ct.tag = ? AND (ct.expires_at IS NULL OR ct.expires_at > ?)))`)
		now := time.Now().UTC().UnixMilli()
		args = append(args, params.Tag, now, params.Tag, now)
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
	if params.Tag != "" {
		where = append(where, "1 = 0")
	}
	if params.IP != "" {
		where = append(where, "client_ip = ?")
		args = append(args, params.IP)
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

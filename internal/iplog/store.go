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

var allowedSortColumns = map[string]string{
	"recorded_at": "recorded_at",
	"bytes_up":    "bytes_up",
	"bytes_down":  "bytes_down",
	"bytes_total": "(bytes_up + bytes_down)",
	"duration_ms": "duration_ms",
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

func (s *Store) Prune(olderThan time.Time) (int64, error) {
	result, err := s.writeDB.Exec(`DELETE FROM ip_log WHERE recorded_at < ?`, olderThan.Unix())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) Stats() (StoreStats, error) {
	var stats StoreStats
	if err := s.readDB.QueryRow(`SELECT COUNT(*), COALESCE(MIN(recorded_at), 0), COALESCE(MAX(recorded_at), 0) FROM ip_log`).Scan(
		&stats.RecordCount,
		&stats.OldestRecordAt,
		&stats.NewestRecordAt,
	); err != nil {
		return StoreStats{}, err
	}
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

func (s *Store) queryWithoutCIDR(params QueryParams) (QueryResult, error) {
	where, args := buildWhereClause(params)
	countQuery := `SELECT COUNT(*) FROM ip_log` + where
	var total int
	if err := s.readDB.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return QueryResult{}, err
	}

	query := `SELECT id, ip, asn, as_org, country, protocol, upstream, port, bytes_up, bytes_down, duration_ms, recorded_at FROM ip_log` + where + orderClause(params) + ` LIMIT ? OFFSET ?`
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
	_, network, err := net.ParseCIDR(params.CIDR)
	if err != nil {
		return QueryResult{}, err
	}
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
	filtered := make([]Record, 0, len(all))
	for _, record := range all {
		ip := net.ParseIP(record.IP)
		if ip == nil {
			continue
		}
		if network.Contains(ip) {
			filtered = append(filtered, record)
		}
	}
	sortRecords(filtered, params)

	total := len(filtered)
	start := params.Offset
	if start > total {
		start = total
	}
	end := start + params.Limit
	if end > total {
		end = total
	}
	return QueryResult{
		Total:   total,
		Records: filtered[start:end],
	}, nil
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
	if out.Limit == 0 {
		out.Limit = DefaultQueryLimit
	}
	if out.Limit < 0 || out.Limit > MaxQueryLimit {
		return QueryParams{}, fmt.Errorf("limit must be in 1..%d", MaxQueryLimit)
	}
	if out.Offset < 0 {
		return QueryParams{}, errors.New("offset must be >= 0")
	}
	out.Country = strings.ToUpper(strings.TrimSpace(out.Country))
	out.CIDR = strings.TrimSpace(out.CIDR)
	if out.CIDR != "" && out.StartTime == nil && out.EndTime == nil {
		return QueryParams{}, errors.New("cidr filter requires start_time or end_time")
	}
	if out.StartTime != nil && out.EndTime != nil && *out.EndTime < *out.StartTime {
		return QueryParams{}, errors.New("end_time must be >= start_time")
	}
	if out.CIDR != "" {
		if _, _, err := net.ParseCIDR(out.CIDR); err != nil {
			return QueryParams{}, fmt.Errorf("invalid cidr: %w", err)
		}
	}
	out.SortBy = strings.TrimSpace(out.SortBy)
	if out.SortBy == "" {
		out.SortBy = "recorded_at"
	}
	if _, ok := allowedSortColumns[out.SortBy]; !ok {
		return QueryParams{}, errors.New("invalid sort_by: must be one of recorded_at, bytes_up, bytes_down, bytes_total, duration_ms")
	}
	out.SortOrder = strings.TrimSpace(out.SortOrder)
	if out.SortOrder == "" {
		out.SortOrder = "desc"
	}
	if out.SortOrder != "asc" && out.SortOrder != "desc" {
		return QueryParams{}, errors.New("invalid sort_order: must be asc or desc")
	}
	return out, nil
}

func orderClause(params QueryParams) string {
	direction := "DESC"
	if params.SortOrder == "asc" {
		direction = "ASC"
	}
	return fmt.Sprintf(" ORDER BY %s %s, id %s", allowedSortColumns[params.SortBy], direction, direction)
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

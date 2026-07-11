package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	for _, query := range []string{`DELETE FROM flows WHERE ended_at < ?`, `DELETE FROM flow_entities WHERE ended_at IS NOT NULL AND ended_at < ?`, `DELETE FROM client_tags WHERE expires_at IS NOT NULL AND expires_at < ?`, `DELETE FROM online_rules WHERE enabled = 0 AND expires_at IS NOT NULL AND expires_at < ?`, `DELETE FROM online_rule_events WHERE occurred_at < ?`, `DELETE FROM rejection_events WHERE recorded_at < ?`, `DELETE FROM ip_log WHERE recorded_at < ?`, `DELETE FROM rejection_log WHERE recorded_at < ?`} {
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
		if !strings.Contains(query, "flow_entities") && !strings.Contains(query, "client_tags") {
			deleted += count
		}
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

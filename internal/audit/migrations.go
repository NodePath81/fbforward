package audit

import (
	"database/sql"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

const currentSchemaVersion = 2

var schemaV2Statements = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        name TEXT NOT NULL,
        applied_at INTEGER NOT NULL
    )`,
	`CREATE TABLE IF NOT EXISTS flows (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        flow_id TEXT NOT NULL UNIQUE,
        protocol TEXT NOT NULL,
        client_ip TEXT NOT NULL,
        client_port INTEGER NOT NULL DEFAULT 0,
        client_ip_bytes BLOB NOT NULL DEFAULT X'',
        client_ip_family INTEGER NOT NULL DEFAULT 0,
        listener TEXT NOT NULL DEFAULT '',
        route TEXT NOT NULL DEFAULT '',
        upstream TEXT NOT NULL DEFAULT '',
        started_at INTEGER NOT NULL,
        ended_at INTEGER NOT NULL,
        last_activity_at INTEGER NOT NULL,
        bytes_up INTEGER NOT NULL DEFAULT 0,
        bytes_down INTEGER NOT NULL DEFAULT 0,
        close_reason TEXT NOT NULL DEFAULT '',
        fingerprint TEXT NOT NULL DEFAULT '',
        policy_version TEXT NOT NULL DEFAULT '',
        rule_id TEXT NOT NULL DEFAULT '',
        asn INTEGER,
        as_org TEXT,
        country TEXT
    )`,
	`CREATE TABLE IF NOT EXISTS flow_checkpoints (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        flow_id TEXT NOT NULL,
        recorded_at INTEGER NOT NULL,
        last_activity_at INTEGER NOT NULL,
        bytes_up INTEGER NOT NULL DEFAULT 0,
        bytes_down INTEGER NOT NULL DEFAULT 0,
        segments_up INTEGER NOT NULL DEFAULT 0,
        segments_down INTEGER NOT NULL DEFAULT 0,
        FOREIGN KEY(flow_id) REFERENCES flows(flow_id) ON DELETE CASCADE
    )`,
	`CREATE TABLE IF NOT EXISTS rejection_events (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        event_id TEXT NOT NULL UNIQUE,
        protocol TEXT NOT NULL,
        client_ip TEXT NOT NULL,
        client_port INTEGER NOT NULL DEFAULT 0,
        client_ip_bytes BLOB NOT NULL DEFAULT X'',
        client_ip_family INTEGER NOT NULL DEFAULT 0,
        listener TEXT NOT NULL DEFAULT '',
        port INTEGER NOT NULL DEFAULT 0,
        reason TEXT NOT NULL,
        matched_rule_type TEXT NOT NULL DEFAULT '',
        matched_rule_value TEXT NOT NULL DEFAULT '',
        policy_version TEXT NOT NULL DEFAULT '',
        rule_id TEXT NOT NULL DEFAULT '',
        recorded_at INTEGER NOT NULL,
        asn INTEGER,
        as_org TEXT,
        country TEXT
    )`,
	`CREATE TABLE IF NOT EXISTS flow_tag_events (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        event_id TEXT NOT NULL UNIQUE,
        flow_id TEXT NOT NULL,
        tag TEXT NOT NULL,
        operation TEXT NOT NULL,
        source TEXT NOT NULL DEFAULT '',
        actor TEXT NOT NULL DEFAULT '',
        expires_at INTEGER,
        created_at INTEGER NOT NULL,
        metadata TEXT NOT NULL DEFAULT '',
        FOREIGN KEY(flow_id) REFERENCES flows(flow_id) ON DELETE CASCADE
    )`,
	`CREATE TABLE IF NOT EXISTS flow_tags (
        flow_id TEXT NOT NULL,
        tag TEXT NOT NULL,
        source TEXT NOT NULL DEFAULT '',
        expires_at INTEGER,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        PRIMARY KEY(flow_id, tag),
        FOREIGN KEY(flow_id) REFERENCES flows(flow_id) ON DELETE CASCADE
    )`,
	`CREATE TABLE IF NOT EXISTS client_tags (
        client_ip TEXT NOT NULL,
        tag TEXT NOT NULL,
        source TEXT NOT NULL DEFAULT '',
        expires_at INTEGER,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        PRIMARY KEY(client_ip, tag)
    )`,
	`CREATE TABLE IF NOT EXISTS online_rules (
        rule_id TEXT PRIMARY KEY,
        version TEXT NOT NULL DEFAULT '',
        action TEXT NOT NULL,
        rule_type TEXT NOT NULL,
        rule_value TEXT NOT NULL,
        protocol TEXT NOT NULL DEFAULT '',
        port INTEGER,
        enabled INTEGER NOT NULL DEFAULT 1,
        expires_at INTEGER,
        source TEXT NOT NULL DEFAULT '',
        payload_json TEXT NOT NULL DEFAULT '',
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
    )`,
	`CREATE TABLE IF NOT EXISTS policy_events (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        event_id TEXT NOT NULL UNIQUE,
        flow_id TEXT,
        client_ip TEXT NOT NULL DEFAULT '',
        policy_version TEXT NOT NULL DEFAULT '',
        rule_id TEXT NOT NULL DEFAULT '',
        decision TEXT NOT NULL,
        rule_type TEXT NOT NULL DEFAULT '',
        rule_value TEXT NOT NULL DEFAULT '',
        reason TEXT NOT NULL DEFAULT '',
        occurred_at INTEGER NOT NULL,
        FOREIGN KEY(flow_id) REFERENCES flows(flow_id) ON DELETE SET NULL
    )`,
	`CREATE INDEX IF NOT EXISTS idx_flows_started_at ON flows(started_at)`,
	`CREATE INDEX IF NOT EXISTS idx_flows_client_time ON flows(client_ip, started_at)`,
	`CREATE INDEX IF NOT EXISTS idx_flows_client_bytes ON flows(client_ip_bytes, client_ip_family, started_at)`,
	`CREATE INDEX IF NOT EXISTS idx_flows_protocol_time ON flows(protocol, started_at)`,
	`CREATE INDEX IF NOT EXISTS idx_flows_upstream_time ON flows(upstream, started_at)`,
	`CREATE INDEX IF NOT EXISTS idx_flow_checkpoints_flow_time ON flow_checkpoints(flow_id, recorded_at)`,
	`CREATE INDEX IF NOT EXISTS idx_rejections_time ON rejection_events(recorded_at)`,
	`CREATE INDEX IF NOT EXISTS idx_rejections_client_time ON rejection_events(client_ip, recorded_at)`,
	`CREATE INDEX IF NOT EXISTS idx_rejections_client_bytes ON rejection_events(client_ip_bytes, client_ip_family, recorded_at)`,
	`CREATE INDEX IF NOT EXISTS idx_rejections_protocol_time ON rejection_events(protocol, recorded_at)`,
	`CREATE INDEX IF NOT EXISTS idx_flow_tag_events_flow_time ON flow_tag_events(flow_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_flow_tags_tag_flow ON flow_tags(tag, flow_id)`,
	`CREATE INDEX IF NOT EXISTS idx_flow_tags_expiry ON flow_tags(expires_at)`,
	`CREATE INDEX IF NOT EXISTS idx_client_tags_tag ON client_tags(tag)`,
	`CREATE INDEX IF NOT EXISTS idx_client_tags_expiry ON client_tags(expires_at)`,
	`CREATE INDEX IF NOT EXISTS idx_online_rules_enabled_expiry ON online_rules(enabled, expires_at)`,
	`CREATE INDEX IF NOT EXISTS idx_policy_events_flow_time ON policy_events(flow_id, occurred_at)`,
}

type migrationHook func(step int, statement string) error

func migrateDB(db *sql.DB) error {
	return migrateDBWithHook(db, nil)
}

func migrateDBWithHook(db *sql.DB, hook migrationHook) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("unsupported sqlite schema version %d (maximum %d)", version, currentSchemaVersion)
	}
	if version == currentSchemaVersion {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite migration: %w", err)
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	for i, statement := range schemaV2Statements {
		if _, err := tx.Exec(statement); err != nil {
			return rollback(fmt.Errorf("migration statement %d: %w", i, err))
		}
		if hook != nil {
			if err := hook(i, statement); err != nil {
				return rollback(err)
			}
		}
	}
	if err := migrateLegacyRows(tx); err != nil {
		return rollback(err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`, currentSchemaVersion, "audit schema v2", now); err != nil {
		return rollback(fmt.Errorf("record sqlite migration: %w", err))
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, currentSchemaVersion)); err != nil {
		return rollback(fmt.Errorf("set sqlite schema version: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite migration: %w", err)
	}
	return nil
}

func migrateLegacyRows(tx *sql.Tx) error {
	legacyFlow, err := tableExists(tx, "ip_log")
	if err != nil {
		return err
	}
	if legacyFlow {
		rows, err := tx.Query(`SELECT id, ip, asn, as_org, country, protocol, upstream, port, bytes_up, bytes_down, duration_ms, recorded_at FROM ip_log ORDER BY id`)
		if err != nil {
			return fmt.Errorf("read legacy ip_log: %w", err)
		}
		stmt, err := tx.Prepare(`INSERT OR IGNORE INTO flows(flow_id, protocol, client_ip, client_port, client_ip_bytes, client_ip_family, listener, route, upstream, started_at, ended_at, last_activity_at, bytes_up, bytes_down, close_reason, fingerprint, policy_version, rule_id, asn, as_org, country) VALUES (?, ?, ?, 0, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, 'legacy_migrated', '', '', '', ?, ?, ?)`)
		if err != nil {
			rows.Close()
			return err
		}
		for rows.Next() {
			var id, port, bytesUp, bytesDown, duration, recorded int64
			var asn sql.NullInt64
			var ip, protocol, upstream string
			var asOrg, country sql.NullString
			if err := rows.Scan(&id, &ip, &asn, &asOrg, &country, &protocol, &upstream, &port, &bytesUp, &bytesDown, &duration, &recorded); err != nil {
				stmt.Close()
				rows.Close()
				return fmt.Errorf("scan legacy ip_log: %w", err)
			}
			blob, family, err := ipBytes(ip)
			if err != nil {
				stmt.Close()
				rows.Close()
				return fmt.Errorf("parse legacy ip_log address %q: %w", ip, err)
			}
			ended := recorded * 1000
			started := ended - duration
			if started < 0 {
				started = 0
			}
			listener := fmt.Sprintf(":%d", port)
			if _, err := stmt.Exec(fmt.Sprintf("legacy-ip-log:%d", id), protocol, ip, blob, family, listener, upstream, started, ended, ended, bytesUp, bytesDown, nullableInt64(asn), nullableString(asOrg), nullableString(country)); err != nil {
				stmt.Close()
				rows.Close()
				return fmt.Errorf("migrate legacy ip_log row %d: %w", id, err)
			}
		}
		stmt.Close()
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}

	legacyRejection, err := tableExists(tx, "rejection_log")
	if err != nil {
		return err
	}
	if legacyRejection {
		rows, err := tx.Query(`SELECT id, ip, asn, as_org, country, protocol, port, reason, matched_rule_type, matched_rule_value, recorded_at FROM rejection_log ORDER BY id`)
		if err != nil {
			return fmt.Errorf("read legacy rejection_log: %w", err)
		}
		stmt, err := tx.Prepare(`INSERT OR IGNORE INTO rejection_events(event_id, protocol, client_ip, client_port, client_ip_bytes, client_ip_family, listener, port, reason, matched_rule_type, matched_rule_value, policy_version, rule_id, recorded_at, asn, as_org, country) VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?)`)
		if err != nil {
			rows.Close()
			return err
		}
		for rows.Next() {
			var id, port, recorded int64
			var asn sql.NullInt64
			var ip, protocol, reason string
			var asOrg, country, ruleType, ruleValue sql.NullString
			if err := rows.Scan(&id, &ip, &asn, &asOrg, &country, &protocol, &port, &reason, &ruleType, &ruleValue, &recorded); err != nil {
				stmt.Close()
				rows.Close()
				return fmt.Errorf("scan legacy rejection_log: %w", err)
			}
			blob, family, err := ipBytes(ip)
			if err != nil {
				stmt.Close()
				rows.Close()
				return fmt.Errorf("parse legacy rejection address %q: %w", ip, err)
			}
			if _, err := stmt.Exec(fmt.Sprintf("legacy-rejection:%d", id), protocol, ip, blob, family, fmt.Sprintf(":%d", port), port, reason, nullableString(ruleType), nullableString(ruleValue), recorded*1000, nullableInt64(asn), nullableString(asOrg), nullableString(country)); err != nil {
				stmt.Close()
				rows.Close()
				return fmt.Errorf("migrate legacy rejection row %d: %w", id, err)
			}
		}
		stmt.Close()
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func tableExists(tx *sql.Tx, name string) (bool, error) {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func ipBytes(raw string) ([]byte, int, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return nil, 0, err
	}
	if addr.Is4() {
		v := addr.As4()
		return v[:], 4, nil
	}
	v := addr.As16()
	return v[:], 6, nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func nullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

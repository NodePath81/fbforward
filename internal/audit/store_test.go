package audit

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestEmptyStoreInitializesSchemaV2(t *testing.T) {
	store := newTestStore(t)
	var version int
	if err := store.readDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	for _, table := range []string{"flows", "flow_checkpoints", "rejection_events", "flow_tag_events", "flow_tags", "client_tags", "online_rules", "policy_events", "schema_migrations", "ip_log", "rejection_log"} {
		var count int
		if err := store.readDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if table == "ip_log" || table == "rejection_log" {
			if count != 0 {
				t.Fatalf("unexpected legacy table %s in empty database", table)
			}
		} else if count != 1 {
			t.Fatalf("missing table %s", table)
		}
	}
}

func TestLegacyDatabaseMigratesIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE ip_log (id INTEGER PRIMARY KEY AUTOINCREMENT, ip TEXT NOT NULL, asn INTEGER, as_org TEXT, country TEXT, protocol TEXT NOT NULL, upstream TEXT NOT NULL, port INTEGER NOT NULL, bytes_up INTEGER NOT NULL, bytes_down INTEGER NOT NULL, duration_ms INTEGER NOT NULL, recorded_at INTEGER NOT NULL); CREATE TABLE rejection_log (id INTEGER PRIMARY KEY AUTOINCREMENT, ip TEXT NOT NULL, asn INTEGER, as_org TEXT, country TEXT, protocol TEXT NOT NULL, port INTEGER NOT NULL, reason TEXT NOT NULL, matched_rule_type TEXT, matched_rule_value TEXT, recorded_at INTEGER NOT NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO ip_log(ip, asn, as_org, country, protocol, upstream, port, bytes_up, bytes_down, duration_ms, recorded_at) VALUES ('192.0.2.1', 64500, 'example', 'US', 'tcp', 'primary', 9000, 10, 20, 500, 1000); INSERT INTO rejection_log(ip, protocol, port, reason, matched_rule_type, matched_rule_value, recorded_at) VALUES ('198.51.100.1', 'udp', 9001, 'deny', 'cidr', '198.51.100.0/24', 1001);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Query(QueryParams{Limit: 10})
	if err != nil || result.Total != 1 || result.Records[0].FlowID != "legacy-ip-log:1" {
		t.Fatalf("migrated flow = %+v err=%v", result, err)
	}
	rejections, err := store.QueryRejections(RejectionQueryParams{Limit: 10})
	if err != nil || rejections.Total != 1 || rejections.Records[0].EventID != "legacy-rejection:1" {
		t.Fatalf("migrated rejection = %+v err=%v", rejections, err)
	}
	_ = store.Close()

	store, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stats, err := store.Stats()
	if err != nil || stats.FlowRecordCount != 1 || stats.RejectionRecordCount != 1 {
		t.Fatalf("repeat migration stats = %+v err=%v", stats, err)
	}
}

func TestMigrationFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failed.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected migration failure")
	if err := migrateDBWithHook(db, func(step int, _ string) error {
		if step == 3 {
			return failure
		}
		return nil
	}); !errors.Is(err, failure) {
		t.Fatalf("migration error = %v", err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Fatalf("rolled back version = %d", version)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='flows'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("migration left flows table after rollback")
	}
	_ = db.Close()
}

func TestFlowRejectionTagsAndTopTalkers(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.InsertFlows([]FlowRecord{
		{FlowID: "f1", Protocol: "tcp", ClientIP: "192.0.2.1", Listener: ":9000", Upstream: "a", StartedAt: now.Add(-time.Second), EndedAt: now, LastActivity: now, BytesUp: 10, BytesDown: 20},
		{FlowID: "f2", Protocol: "udp", ClientIP: "192.0.2.1", Listener: ":9000", Upstream: "a", StartedAt: now.Add(-time.Second), EndedAt: now, LastActivity: now, BytesUp: 5, BytesDown: 5},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRejections([]RejectionRow{{EventID: "r1", Protocol: "tcp", ClientIP: "198.51.100.1", Reason: "deny", RecordedAt: now}}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFlowTagEvent(FlowTagEvent{EventID: "te1", FlowID: "f1", Tag: "trusted", Operation: "add"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(FlowTag{FlowID: "f1", Tag: "trusted"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertClientTag(ClientTag{ClientIP: "192.0.2.1", Tag: "customer"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertOnlineRule(OnlineRule{RuleID: "rule-1", Action: "deny", RuleType: "cidr", RuleValue: "198.51.100.0/24", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPolicyEvent(PolicyEvent{EventID: "pe1", FlowID: "f1", Decision: "allow", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if tags, err := store.QueryFlowTags("f1"); err != nil || len(tags) != 1 || tags[0].Tag != "trusted" {
		t.Fatalf("flow tags = %+v err=%v", tags, err)
	}
	if tags, err := store.QueryClientTags("192.0.2.1"); err != nil || len(tags) != 1 || tags[0].Tag != "customer" {
		t.Fatalf("client tags = %+v err=%v", tags, err)
	}
	if events, err := store.QueryPolicyEvents("f1"); err != nil || len(events) != 1 || events[0].Decision != "allow" {
		t.Fatalf("policy events = %+v err=%v", events, err)
	}
	talkers, err := store.GetTopTalkers(TopTalkerParams{Limit: 10})
	if err != nil || len(talkers) == 0 || talkers[0].ClientIP != "192.0.2.1" || talkers[0].BytesTotal != 40 {
		t.Fatalf("top talkers = %+v err=%v", talkers, err)
	}
}

func TestBackupRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertFlows([]FlowRecord{{FlowID: "backup-flow", Protocol: "tcp", ClientIP: "192.0.2.5", EndedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup.sqlite")
	if err := store.Backup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBackup(backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored.sqlite")
	if err := Restore(backup, restored); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	result, err := reopened.Query(QueryParams{Limit: 10})
	if err != nil || result.Total != 1 {
		t.Fatalf("restored query = %+v err=%v", result, err)
	}
	_ = store.Close()
}

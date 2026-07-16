package audit

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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

func TestEmptyStoreInitializesSchemaV4(t *testing.T) {
	store := newTestStore(t)
	var version int
	if err := store.readDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	for _, table := range []string{"flows", "flow_entities", "flow_checkpoints", "rejection_events", "flow_tag_events", "flow_tags", "client_tags", "online_rules", "online_rule_events", "policy_events", "schema_migrations", "ip_log", "rejection_log"} {
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

func TestSchemaV3MigratesToV4OnlineRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range schemaV2Statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaV3(tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO online_rules(rule_id, version, action, rule_type, rule_value, created_at, updated_at) VALUES ('legacy-rule', '1', 'deny', 'cidr', '198.51.100.0/24', 1, 1)`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`PRAGMA user_version = 3`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("schema version = %d, want 4", version)
	}
	for _, column := range []string{"priority", "created_by", "reason", "ticket_ref", "matcher_json", "params_json"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('online_rules') WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("missing online_rules.%s", column)
		}
	}
	var eventTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='online_rule_events'`).Scan(&eventTable); err != nil {
		t.Fatal(err)
	}
	if eventTable != 1 {
		t.Fatal("missing online_rule_events")
	}
	var source string
	if err := db.QueryRow(`SELECT source FROM online_rules WHERE rule_id = 'legacy-rule'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "legacy" {
		t.Fatalf("legacy source = %q, want legacy", source)
	}
	_ = db.Close()
}

func TestOnlineRuleStoreLifecycleAndAudit(t *testing.T) {
	store := newTestStore(t)
	expires := time.Now().UTC().Add(time.Hour)
	rule := OnlineRule{RuleID: "online-1", Action: "deny", RuleType: "source_cidr", RuleValue: "198.51.100.0/24", Priority: 10, Enabled: true, ExpiresAt: &expires, CreatedBy: "control:test", Reason: "incident", TicketRef: "INC-1", MatcherJSON: `{"source_cidr":"198.51.100.0/24"}`}
	if err := store.CreateOnlineRule(rule, OnlineRuleEvent{Operation: "create", Actor: "control:test"}); err != nil {
		t.Fatal(err)
	}
	rules, err := store.ListOnlineRules(time.Now().UTC(), false)
	if err != nil || len(rules) != 1 || rules[0].RuleID != rule.RuleID {
		t.Fatalf("active rules=%+v err=%v", rules, err)
	}
	if err := store.ExpireOnlineRule(rule.RuleID, time.Now().UTC(), OnlineRuleEvent{Operation: "expire", Actor: "control:test"}); err != nil {
		t.Fatal(err)
	}
	active, err := store.ListOnlineRules(time.Now().UTC(), false)
	if err != nil || len(active) != 0 {
		t.Fatalf("expired rule remained active: %+v err=%v", active, err)
	}
	all, err := store.ListOnlineRules(time.Now().UTC(), true)
	if err != nil || len(all) != 1 || all[0].Enabled {
		t.Fatalf("all rules=%+v err=%v", all, err)
	}
	events, err := store.QueryOnlineRuleEvents(rule.RuleID)
	if err != nil || len(events) != 2 || events[0].Operation != "create" || events[1].Operation != "expire" {
		t.Fatalf("online rule events=%+v err=%v", events, err)
	}
	for _, event := range events {
		if event.PayloadJSON == "" || !strings.Contains(event.PayloadJSON, "online-1") {
			t.Fatalf("event payload missing rule snapshot: %+v", event)
		}
	}
	if err := store.DeleteOnlineRule(rule.RuleID, OnlineRuleEvent{Operation: "delete", Actor: "control:test"}); err != nil {
		t.Fatal(err)
	}
	events, err = store.QueryOnlineRuleEvents(rule.RuleID)
	if err != nil || len(events) != 3 || events[2].Operation != "delete" {
		t.Fatalf("delete event missing: %+v err=%v", events, err)
	}
	if events[2].PayloadJSON == "" || !strings.Contains(events[2].PayloadJSON, "online-1") {
		t.Fatalf("delete event payload missing rule snapshot: %+v", events[2])
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

func TestSchemaV3MigratesV2TagTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range schemaV2Statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO flows(flow_id, protocol, client_ip, started_at, ended_at, last_activity_at) VALUES ('v2-flow', 'tcp', '192.0.2.1', 1, 2, 2); INSERT INTO flow_tags(flow_id, tag, created_at, updated_at) VALUES ('v2-flow', 'app:owner=legacy', 2, 2); PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatal(err)
	}
	var entityCount, tagCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM flow_entities WHERE flow_id = 'v2-flow'`).Scan(&entityCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM flow_tags WHERE flow_id = 'v2-flow'`).Scan(&tagCount); err != nil {
		t.Fatal(err)
	}
	if entityCount != 1 || tagCount != 1 {
		t.Fatalf("migrated entity/tag counts = %d/%d", entityCount, tagCount)
	}
	var foreignTable string
	if err := db.QueryRow(`SELECT "table" FROM pragma_foreign_key_list('flow_tags') WHERE "table" = 'flow_entities'`).Scan(&foreignTable); err != nil {
		t.Fatalf("flow_tags foreign key = %v", err)
	}
	_ = db.Close()
}

func TestFlowEntityUpdatesAreMonotonic(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	newLocal := "10.0.0.1:43122"
	oldLocal := "10.0.0.1:43121"
	if err := store.UpsertFlowEntity(FlowEntity{FlowID: "entity-1", Protocol: "tcp", ClientIP: "192.0.2.1", CreatedAt: now, State: "active", Generation: 2, BackendKey: "primary@new", BackendProtocol: "tcp", BackendLocal: newLocal, BackendRemote: "192.0.2.10:443", LastActivity: now, BytesUp: 20}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowEntity(FlowEntity{FlowID: "entity-1", Protocol: "tcp", ClientIP: "192.0.2.1", CreatedAt: now.Add(-time.Minute), State: "active", Generation: 1, BackendKey: "primary@old", BackendProtocol: "tcp", BackendLocal: oldLocal, BackendRemote: "192.0.2.10:443", LastActivity: now.Add(-time.Minute), BytesUp: 1}); err != nil {
		t.Fatal(err)
	}
	ended := now.Add(time.Second)
	resolveUntil := ended.Add(30 * time.Second)
	if err := store.UpsertFlowEntity(FlowEntity{FlowID: "entity-1", Protocol: "tcp", ClientIP: "192.0.2.1", CreatedAt: now, State: "closed", Generation: 2, EndedAt: &ended, ResolveUntil: &resolveUntil, LastActivity: ended, BytesUp: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowEntity(FlowEntity{FlowID: "entity-1", Protocol: "tcp", ClientIP: "192.0.2.1", CreatedAt: now, State: "active", Generation: 3, BackendKey: "primary@newer", BackendLocal: "10.0.0.1:43123", LastActivity: now.Add(-time.Minute), BytesUp: 2}); err != nil {
		t.Fatal(err)
	}
	var state, backendKey, backendLocal string
	var generation, bytesUp, resolve int64
	if err := store.readDB.QueryRow(`SELECT state, backend_key, backend_local, generation, bytes_up, resolve_until FROM flow_entities WHERE flow_id = 'entity-1'`).Scan(&state, &backendKey, &backendLocal, &generation, &bytesUp, &resolve); err != nil {
		t.Fatal(err)
	}
	if state != "closed" || backendKey != "primary@new" || backendLocal != newLocal || generation != 3 || bytesUp != 30 || resolve != resolveUntil.UnixMilli() {
		t.Fatalf("non-monotonic entity row: state=%s backend=%s local=%s generation=%d bytes=%d resolve=%d", state, backendKey, backendLocal, generation, bytesUp, resolve)
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
	if err := store.UpsertClientTag(ClientTag{ClientIP: "192.0.2.1", Tag: "trusted"}); err != nil {
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
	result, err := store.Query(QueryParams{Tag: "trusted", Limit: 10})
	if err != nil || result.Total != 2 || len(result.Records) != 2 {
		t.Fatalf("tag flow query=%+v err=%v", result, err)
	}
	logResult, err := store.QueryLogEvents(LogEventQueryParams{Tag: "trusted", EntryType: EntryTypeFlow, Limit: 10})
	for _, record := range logResult.Records {
		trustedCount := 0
		for _, tag := range record.Tags {
			if tag == "trusted" {
				trustedCount++
			}
		}
		if trustedCount != 1 {
			t.Fatalf("duplicate effective audit tags=%+v err=%v", logResult.Records, err)
		}
	}
	if result, err := store.Query(QueryParams{Tag: "customer", Limit: 10}); err != nil || result.Total != 2 {
		t.Fatalf("client tag flow query=%+v err=%v", result, err)
	}
	if talkers, err := store.GetTopTalkers(TopTalkerParams{Tag: "trusted", Limit: 10}); err != nil || len(talkers) != 1 || talkers[0].ClientIP != "192.0.2.1" {
		t.Fatalf("tag top talkers=%+v err=%v", talkers, err)
	}
	if tags, err := store.QueryClientTags("192.0.2.1"); err != nil || len(tags) != 2 || tags[0].Tag != "customer" || tags[1].Tag != "trusted" {
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

func TestTopASNsAggregatesFlowBytes(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "192.0.2.1", Protocol: "tcp", BytesUp: 10, BytesDown: 20, RecordedAt: now}, ASN: 64500, ASOrg: "Example A", Country: "US"},
		{CloseEvent: CloseEvent{IP: "192.0.2.2", Protocol: "udp", BytesUp: 40, BytesDown: 0, RecordedAt: now}, ASN: 64500, ASOrg: "Example A", Country: "US"},
		{CloseEvent: CloseEvent{IP: "192.0.2.3", Protocol: "tcp", BytesUp: 1, BytesDown: 2, RecordedAt: now}, ASN: 64501, ASOrg: "Example B", Country: "GB"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.GetTopASNs(TopASNParams{Protocol: "tcp", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ASN != 64500 || rows[0].BytesTotal != 30 || rows[0].FlowCount != 1 {
		t.Fatalf("unexpected top ASNs: %#v", rows)
	}
}

func TestTopASNsCombinesCountriesPerASN(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "192.0.2.10", Protocol: "tcp", BytesUp: 10, BytesDown: 5, RecordedAt: now}, ASN: 64510, ASOrg: "Org A", Country: "US"},
		{CloseEvent: CloseEvent{IP: "192.0.2.11", Protocol: "tcp", BytesUp: 20, BytesDown: 5, RecordedAt: now}, ASN: 64510, ASOrg: "Org B", Country: "GB"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.GetTopASNs(TopASNParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ASN != 64510 || rows[0].BytesTotal != 40 || rows[0].FlowCount != 2 || rows[0].Country != "" {
		t.Fatalf("unexpected combined ASN: %#v", rows)
	}
}

func TestTopTagsUsesCurrentMappingsAndDeduplicatesFlowAndClient(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{FlowID: "tag-flow-1", Protocol: "tcp", IP: "192.0.2.1", BytesUp: 10, BytesDown: 20, RecordedAt: now}},
		{CloseEvent: CloseEvent{FlowID: "tag-flow-2", Protocol: "tcp", IP: "192.0.2.2", BytesUp: 5, BytesDown: 5, RecordedAt: now}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(FlowTag{FlowID: "tag-flow-1", Tag: "app:user=alice"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertClientTag(ClientTag{ClientIP: "192.0.2.1", Tag: "app:user=alice"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertClientTag(ClientTag{ClientIP: "192.0.2.2", Tag: "app:user=alice"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(FlowTag{FlowID: "tag-flow-2", Tag: "expired", ExpiresAt: func() *time.Time { v := now.Add(-time.Minute); return &v }()}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.GetTopTags(TopTagParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Tag != "app:user=alice" || rows[0].BytesTotal != 40 || rows[0].FlowCount != 2 {
		t.Fatalf("unexpected top tags: %#v", rows)
	}
}

func TestCurrentTagsAndActionsPageQueries(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	if err := store.UpsertFlowEntity(FlowEntity{FlowID: "context-flow", Protocol: "tcp", ClientIP: "192.0.2.9", CreatedAt: now, LastActivity: now, State: "closed"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(FlowTag{FlowID: "context-flow", Tag: "app:test", Source: "flow-context", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(FlowTag{FlowID: "context-flow", Tag: "literal%_tag", Source: "flow-context", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFlowTagEvent(FlowTagEvent{EventID: "context-event", FlowID: "context-flow", Tag: "app:test", Operation: "set", Actor: "backend", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFlowTagEvent(FlowTagEvent{EventID: "literal-event", FlowID: "context-flow", Tag: "literal%_action", Operation: "set", Actor: "literal%_actor", CreatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	tags, more, err := store.QueryCurrentTags("app:", "flow", 10, 0)
	if err != nil || more || len(tags) != 1 || tags[0].Tag != "app:test" || tags[0].Scope != "flow" {
		t.Fatalf("current tags = %#v more=%v err=%v", tags, more, err)
	}
	tags, more, err = store.QueryCurrentTags("%_", "flow", 10, 0)
	if err != nil || more || len(tags) != 1 || tags[0].Tag != "literal%_tag" {
		t.Fatalf("literal tag search = %#v more=%v err=%v", tags, more, err)
	}
	actions, more, err := store.QueryTagActions("backend", 10, 0)
	if err != nil || more || len(actions) != 1 || actions[0].Actor != "backend" || actions[0].Operation != "set" {
		t.Fatalf("actions = %#v more=%v err=%v", actions, more, err)
	}
	actions, more, err = store.QueryTagActions("%_", 10, 0)
	if err != nil || more || len(actions) != 1 || actions[0].Tag != "literal%_action" {
		t.Fatalf("literal action search = %#v more=%v err=%v", actions, more, err)
	}
}

func TestTopQueriesValidateBounds(t *testing.T) {
	store := newTestStore(t)
	start, end := int64(20), int64(10)
	if _, err := store.GetTopTalkers(TopTalkerParams{StartTime: &start, EndTime: &end}); err == nil {
		t.Fatal("reversed top talker range accepted")
	}
	if _, err := store.GetTopASNs(TopASNParams{Protocol: "icmp"}); err == nil {
		t.Fatal("invalid top ASN protocol accepted")
	}
	if _, err := store.GetTopASNs(TopASNParams{Limit: MaxQueryLimit + 1}); err == nil {
		t.Fatal("oversized top ASN limit accepted")
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

package iplog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestStoreInsertAndQuery(t *testing.T) {
	store := newTestStore(t)

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]EnrichedRecord{{
		CloseEvent: CloseEvent{
			IP:         "192.168.1.10",
			Protocol:   "tcp",
			Upstream:   "primary",
			Port:       9000,
			BytesUp:    100,
			BytesDown:  200,
			DurationMs: 500,
			RecordedAt: now,
		},
		ASN:     13335,
		ASOrg:   "Cloudflare",
		Country: "US",
	}}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	asn := 13335
	result, err := store.Query(QueryParams{
		ASN:   &asn,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if result.Total != 1 || len(result.Records) != 1 {
		t.Fatalf("unexpected query result: %+v", result)
	}
	if result.Records[0].Country != "US" {
		t.Fatalf("expected country US, got %+v", result.Records[0])
	}
}

func TestStoreStats(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: now.Add(-2 * time.Minute)}},
		{CloseEvent: CloseEvent{IP: "10.0.0.2", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: now}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats error: %v", err)
	}
	if stats.FlowRecordCount != 2 || stats.TotalRecordCount != 2 {
		t.Fatalf("expected record count 2, got %+v", stats)
	}
	if stats.OldestRecordAt != now.Add(-2*time.Minute).Unix() {
		t.Fatalf("unexpected oldest record time: %+v", stats)
	}
	if stats.NewestRecordAt != now.Unix() {
		t.Fatalf("unexpected newest record time: %+v", stats)
	}
}

func TestStoreStatsEmpty(t *testing.T) {
	store := newTestStore(t)
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats error: %v", err)
	}
	if stats.FlowRecordCount != 0 || stats.RejectionRecordCount != 0 || stats.TotalRecordCount != 0 || stats.OldestRecordAt != 0 || stats.NewestRecordAt != 0 {
		t.Fatalf("expected empty stats, got %+v", stats)
	}
}

func TestQueryRequiresTimeBoundForCIDR(t *testing.T) {
	store := newTestStore(t)

	_, err := store.Query(QueryParams{
		CIDR:  "192.168.0.0/16",
		Limit: 10,
	})
	if err == nil {
		t.Fatalf("expected CIDR validation error")
	}
}

func TestPruneRemovesOldRows(t *testing.T) {
	store := newTestStore(t)

	oldTime := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	newTime := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: oldTime}},
		{CloseEvent: CloseEvent{IP: "10.0.0.2", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: newTime}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	deleted, err := store.Prune(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("Prune error: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected one row deleted, got %d", deleted)
	}

	result, err := store.Query(QueryParams{Limit: 10})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if result.Total != 1 || result.Records[0].IP != "10.0.0.2" {
		t.Fatalf("unexpected rows after prune: %+v", result)
	}
}

func TestStoreReopenPreservesSchemaAndData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iplog.sqlite")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	if err := store.InsertBatch([]EnrichedRecord{{CloseEvent: CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: time.Now().UTC()}}}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen NewStore error: %v", err)
	}
	defer reopened.Close()

	result, err := reopened.Query(QueryParams{Limit: 10})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("expected persisted row after reopen, got %+v", result)
	}

	stats, err := reopened.Stats()
	if err != nil {
		t.Fatalf("Stats error: %v", err)
	}
	if stats.FlowRecordCount != 1 || stats.TotalRecordCount != 1 {
		t.Fatalf("expected stats to reflect reopened data, got %+v", stats)
	}
}

func TestInsertBatchEmptyNoOp(t *testing.T) {
	store := newTestStore(t)
	if err := store.InsertBatch(nil); err != nil {
		t.Fatalf("expected empty batch to be a no-op, got %v", err)
	}
}

func TestQueryCombinedFiltersAndPagination(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour).Unix()
	end := now.Add(time.Hour).Unix()
	asn := 13335

	records := []EnrichedRecord{
		{
			CloseEvent: CloseEvent{IP: "192.168.1.10", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: now.Add(-2 * time.Minute)},
			ASN:        13335,
			Country:    "US",
		},
		{
			CloseEvent: CloseEvent{IP: "192.168.1.20", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: now.Add(-time.Minute)},
			ASN:        13335,
			Country:    "US",
		},
		{
			CloseEvent: CloseEvent{IP: "198.51.100.10", Protocol: "udp", Upstream: "b", Port: 2, RecordedAt: now},
			ASN:        64500,
			Country:    "CA",
		},
	}
	if err := store.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	result, err := store.Query(QueryParams{
		StartTime: &start,
		EndTime:   &end,
		ASN:       &asn,
		Country:   "us",
		Limit:     1,
		Offset:    1,
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("expected total before pagination to be 2, got %+v", result)
	}
	if len(result.Records) != 1 || result.Records[0].IP != "192.168.1.10" {
		t.Fatalf("unexpected paginated query result: %+v", result)
	}
}

func TestQueryRejectsInvalidBoundsAndCIDR(t *testing.T) {
	store := newTestStore(t)
	start := time.Now().Unix()
	end := start - 1

	if _, err := store.Query(QueryParams{StartTime: &start, EndTime: &end}); err == nil {
		t.Fatalf("expected invalid time bounds to fail")
	}
	if _, err := store.Query(QueryParams{CIDR: "not-a-cidr", StartTime: &start}); err == nil {
		t.Fatalf("expected invalid CIDR to fail")
	}
}

func TestQueryRejectsInvalidSortParams(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.Query(QueryParams{Limit: 10, SortBy: "invalid"}); err == nil {
		t.Fatalf("expected invalid sort_by to fail")
	}
	if _, err := store.Query(QueryParams{Limit: 10, SortOrder: "sideways"}); err == nil {
		t.Fatalf("expected invalid sort_order to fail")
	}
	if _, err := store.Query(QueryParams{Limit: 10, SortBy: "BYTES_UP"}); err == nil {
		t.Fatalf("expected case-sensitive sort_by to fail")
	}
}

func TestQuerySortsWithoutCIDR(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 1, BytesDown: 100, DurationMs: 30, RecordedAt: now.Add(-3 * time.Minute)}},
		{CloseEvent: CloseEvent{IP: "10.0.0.2", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 5, BytesDown: 10, DurationMs: 20, RecordedAt: now.Add(-2 * time.Minute)}},
		{CloseEvent: CloseEvent{IP: "10.0.0.3", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 3, BytesDown: 50, DurationMs: 10, RecordedAt: now.Add(-time.Minute)}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	result, err := store.Query(QueryParams{Limit: 10, SortBy: "bytes_total", SortOrder: "desc"})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(result.Records) != 3 {
		t.Fatalf("expected 3 records, got %+v", result)
	}
	got := []string{result.Records[0].IP, result.Records[1].IP, result.Records[2].IP}
	want := []string{"10.0.0.1", "10.0.0.3", "10.0.0.2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected sort order: got %v want %v", got, want)
		}
	}

	result, err = store.Query(QueryParams{Limit: 10, SortBy: "bytes_down", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("bytes_down asc Query error: %v", err)
	}
	if got := result.Records[0].IP; got != "10.0.0.2" {
		t.Fatalf("expected smallest bytes_down first, got %+v", result.Records)
	}

	result, err = store.Query(QueryParams{Limit: 10, SortBy: "duration_ms", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("duration_ms asc Query error: %v", err)
	}
	if got := result.Records[0].IP; got != "10.0.0.3" {
		t.Fatalf("expected shortest duration first, got %+v", result.Records)
	}

	result, err = store.Query(QueryParams{Limit: 10, SortBy: "recorded_at", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("recorded_at asc Query error: %v", err)
	}
	if got := result.Records[0].IP; got != "10.0.0.1" {
		t.Fatalf("expected oldest record first, got %+v", result.Records)
	}

	result, err = store.Query(QueryParams{Limit: 10, SortBy: "", SortOrder: ""})
	if err != nil {
		t.Fatalf("defaulted sort Query error: %v", err)
	}
	if got := result.Records[0].IP; got != "10.0.0.3" {
		t.Fatalf("expected recorded_at desc defaults, got %+v", result.Records)
	}
}

func TestQuerySortsWithCIDRAndStablePagination(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour).Unix()

	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "10.0.1.1", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 5, BytesDown: 0, DurationMs: 50, RecordedAt: now.Add(-4 * time.Minute)}},
		{CloseEvent: CloseEvent{IP: "10.0.1.2", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 5, BytesDown: 0, DurationMs: 50, RecordedAt: now.Add(-3 * time.Minute)}},
		{CloseEvent: CloseEvent{IP: "10.0.1.3", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 2, BytesDown: 0, DurationMs: 20, RecordedAt: now.Add(-2 * time.Minute)}},
		{CloseEvent: CloseEvent{IP: "10.0.1.4", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 1, BytesDown: 0, DurationMs: 10, RecordedAt: now.Add(-time.Minute)}},
		{CloseEvent: CloseEvent{IP: "198.51.100.10", Protocol: "tcp", Upstream: "b", Port: 1, BytesUp: 99, BytesDown: 0, DurationMs: 99, RecordedAt: now}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	firstPage, err := store.Query(QueryParams{
		StartTime: &start,
		CIDR:      "10.0.1.0/24",
		SortBy:    "bytes_up",
		SortOrder: "desc",
		Limit:     2,
		Offset:    0,
	})
	if err != nil {
		t.Fatalf("first Query error: %v", err)
	}
	secondPage, err := store.Query(QueryParams{
		StartTime: &start,
		CIDR:      "10.0.1.0/24",
		SortBy:    "bytes_up",
		SortOrder: "desc",
		Limit:     2,
		Offset:    2,
	})
	if err != nil {
		t.Fatalf("second Query error: %v", err)
	}

	if firstPage.Total != 4 || secondPage.Total != 4 {
		t.Fatalf("expected total 4 on both pages, got %+v %+v", firstPage, secondPage)
	}
	if len(firstPage.Records) != 2 || len(secondPage.Records) != 2 {
		t.Fatalf("unexpected page sizes: %+v %+v", firstPage, secondPage)
	}
	if firstPage.Records[0].BytesUp != 5 || firstPage.Records[1].BytesUp != 5 {
		t.Fatalf("expected top page to contain the tied max rows: %+v", firstPage.Records)
	}
	if firstPage.Records[0].ID <= firstPage.Records[1].ID {
		t.Fatalf("expected DESC tiebreaker on id for equal bytes_up: %+v", firstPage.Records)
	}
	if secondPage.Records[0].IP != "10.0.1.3" || secondPage.Records[1].IP != "10.0.1.4" {
		t.Fatalf("unexpected second page order: %+v", secondPage.Records)
	}

	ascPage, err := store.Query(QueryParams{
		StartTime: &start,
		CIDR:      "10.0.1.0/24",
		SortBy:    "recorded_at",
		SortOrder: "asc",
		Limit:     4,
	})
	if err != nil {
		t.Fatalf("recorded_at asc CIDR Query error: %v", err)
	}
	if ascPage.Records[0].IP != "10.0.1.1" || ascPage.Records[3].IP != "10.0.1.4" {
		t.Fatalf("unexpected CIDR recorded_at asc order: %+v", ascPage.Records)
	}

	totalPage, err := store.Query(QueryParams{
		StartTime: &start,
		CIDR:      "10.0.1.0/24",
		SortBy:    "bytes_total",
		SortOrder: "desc",
		Limit:     2,
		Offset:    0,
	})
	if err != nil {
		t.Fatalf("bytes_total CIDR Query error: %v", err)
	}
	if totalPage.Total != 4 {
		t.Fatalf("expected total 4 before pagination, got %+v", totalPage)
	}
	if totalPage.Records[0].ID <= totalPage.Records[1].ID {
		t.Fatalf("expected deterministic id DESC tiebreak for equal bytes_total, got %+v", totalPage.Records)
	}
}

func TestFileSizeGrowsAcrossBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iplog.sqlite")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: now}},
	}); err != nil {
		t.Fatalf("first InsertBatch error: %v", err)
	}
	before, err := store.Stats()
	if err != nil || before.FlowRecordCount != 1 || before.TotalRecordCount != 1 {
		t.Fatalf("expected first stats to succeed, got %+v err=%v", before, err)
	}
	info1, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}

	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "10.0.0.2", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: now.Add(time.Second)}},
		{CloseEvent: CloseEvent{IP: "10.0.0.3", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: now.Add(2 * time.Second)}},
	}); err != nil {
		t.Fatalf("second InsertBatch error: %v", err)
	}
	info2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("second Stat error: %v", err)
	}
	if info2.Size() < info1.Size() {
		t.Fatalf("expected db file size to stay monotonic, before=%d after=%d", info1.Size(), info2.Size())
	}
}

func TestStoreInsertQueryRejectionsAndStats(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "192.168.1.10", Protocol: "tcp", Upstream: "primary", Port: 9000, RecordedAt: now.Add(-time.Minute)}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}
	if err := store.InsertRejectionBatch([]EnrichedRejectionRecord{
		{
			RejectionEvent: RejectionEvent{
				IP:               "10.0.0.1",
				Protocol:         "udp",
				Port:             9000,
				Reason:           "udp_mapping_limit",
				MatchedRuleType:  "",
				MatchedRuleValue: "",
				RecordedAt:       now,
			},
			ASN:     64500,
			Country: "US",
		},
	}); err != nil {
		t.Fatalf("InsertRejectionBatch error: %v", err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats error: %v", err)
	}
	if stats.FlowRecordCount != 1 || stats.RejectionRecordCount != 1 || stats.TotalRecordCount != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	rejections, err := store.QueryRejections(RejectionQueryParams{
		Reason:   "udp_mapping_limit",
		Protocol: "udp",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("QueryRejections error: %v", err)
	}
	if rejections.Total != 1 || len(rejections.Records) != 1 {
		t.Fatalf("unexpected rejection query result: %+v", rejections)
	}
	if rejections.Records[0].Country != "US" || rejections.Records[0].Reason != "udp_mapping_limit" {
		t.Fatalf("unexpected rejection record: %+v", rejections.Records[0])
	}
}

func TestStoreQueryLogEventsMergedAndSortValidation(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour).Unix()

	if err := store.InsertBatch([]EnrichedRecord{
		{CloseEvent: CloseEvent{IP: "192.168.1.10", Protocol: "tcp", Upstream: "primary", Port: 9000, BytesUp: 10, BytesDown: 20, DurationMs: 30, RecordedAt: now.Add(-time.Minute)}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}
	if err := store.InsertRejectionBatch([]EnrichedRejectionRecord{
		{
			RejectionEvent: RejectionEvent{
				IP:               "10.0.0.1",
				Protocol:         "udp",
				Port:             9000,
				Reason:           "firewall_deny",
				MatchedRuleType:  "cidr",
				MatchedRuleValue: "10.0.0.0/8",
				RecordedAt:       now,
			},
			ASN:     64500,
			Country: "US",
		},
	}); err != nil {
		t.Fatalf("InsertRejectionBatch error: %v", err)
	}

	result, err := store.QueryLogEvents(LogEventQueryParams{
		StartTime: &start,
		EntryType: EntryTypeAll,
		SortBy:    "recorded_at",
		SortOrder: "desc",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("QueryLogEvents error: %v", err)
	}
	if result.Total != 2 || len(result.Records) != 2 {
		t.Fatalf("unexpected merged query result: %+v", result)
	}
	if result.Records[0].EntryType != EntryTypeRejection || result.Records[1].EntryType != EntryTypeFlow {
		t.Fatalf("unexpected merged order: %+v", result.Records)
	}
	if result.Records[0].Reason == nil || *result.Records[0].Reason != "firewall_deny" {
		t.Fatalf("expected rejection metadata in merged record, got %+v", result.Records[0])
	}
	if result.Records[1].Upstream == nil || *result.Records[1].Upstream != "primary" {
		t.Fatalf("expected flow metadata in merged record, got %+v", result.Records[1])
	}

	if _, err := store.QueryLogEvents(LogEventQueryParams{
		EntryType: EntryTypeAll,
		SortBy:    "bytes_up",
		Limit:     10,
	}); err == nil {
		t.Fatalf("expected invalid merged sort_by to fail")
	}
}

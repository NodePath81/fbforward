package iplog

import (
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

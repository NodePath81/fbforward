package iplog

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreInsertAndQuery(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	defer store.Close()

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
	store, err := NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	defer store.Close()

	_, err = store.Query(QueryParams{
		CIDR:  "192.168.0.0/16",
		Limit: 10,
	})
	if err == nil {
		t.Fatalf("expected CIDR validation error")
	}
}

func TestPruneRemovesOldRows(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	defer store.Close()

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

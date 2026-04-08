package geoip

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
)

type fakeASNReader struct {
	asn    int
	asOrg  string
	closed bool
}

func (r *fakeASNReader) Lookup(net.IP) (int, string, error) {
	return r.asn, r.asOrg, nil
}

func (r *fakeASNReader) Close() error {
	r.closed = true
	return nil
}

type fakeCountryReader struct {
	country string
	closed  bool
}

func (r *fakeCountryReader) Lookup(net.IP) (string, error) {
	return r.country, nil
}

func (r *fakeCountryReader) Close() error {
	r.closed = true
	return nil
}

func TestLookupSupportsPartialAvailability(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	asn := &fakeASNReader{asn: 13335, asOrg: "Cloudflare"}
	mgr.readers.Store(&readerSet{asn: asn})

	result := mgr.Lookup(net.ParseIP("1.1.1.1"))
	if !result.ASNDBAvailable {
		t.Fatalf("expected ASN DB availability")
	}
	if result.CountryAvailable {
		t.Fatalf("did not expect country DB availability")
	}
	if result.ASN != 13335 || result.ASOrg != "Cloudflare" {
		t.Fatalf("unexpected lookup result: %+v", result)
	}
}

func TestRefreshFailurePreservesExistingReader(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{
		Enabled:         true,
		ASNDBURL:        "https://example.test/GeoLite2-ASN.mmdb",
		ASNDBPath:       "/tmp/GeoLite2-ASN.mmdb",
		RefreshInterval: config.Duration(24 * time.Hour),
	}, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	old := &fakeASNReader{asn: 13335, asOrg: "Cloudflare"}
	mgr.readers.Store(&readerSet{asn: old})
	mgr.download = func(context.Context, string, string) error { return errors.New("boom") }
	mgr.refreshConfigured(context.Background())

	result := mgr.Lookup(net.ParseIP("1.1.1.1"))
	if result.ASN != 13335 || result.ASOrg != "Cloudflare" {
		t.Fatalf("expected existing reader to remain active: %+v", result)
	}
}

func TestRefreshSuccessSwapsReader(t *testing.T) {
	cfg := config.GeoIPConfig{
		Enabled:         true,
		ASNDBURL:        "https://example.test/GeoLite2-ASN.mmdb",
		ASNDBPath:       filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb"),
		RefreshInterval: config.Duration(24 * time.Hour),
	}
	mgr, err := NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	old := &fakeASNReader{asn: 1, asOrg: "old"}
	newReader := &fakeASNReader{asn: 2, asOrg: "new"}
	mgr.readers.Store(&readerSet{asn: old})
	mgr.download = func(context.Context, string, string) error { return nil }
	mgr.openASN = func(string) (asnReader, error) { return newReader, nil }
	mgr.refreshConfigured(context.Background())

	result := mgr.Lookup(net.ParseIP("1.1.1.1"))
	if result.ASN != 2 || result.ASOrg != "new" {
		t.Fatalf("expected new reader after refresh: %+v", result)
	}
	if !old.closed {
		t.Fatalf("expected old reader to be closed")
	}
}

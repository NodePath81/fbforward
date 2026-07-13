package geoip

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
)

type fakeASNReader struct {
	asn    int
	org    string
	closed bool
}

func (r *fakeASNReader) Lookup(net.IP) (int, string, error) { return r.asn, r.org, nil }
func (r *fakeASNReader) Close() error                       { r.closed = true; return nil }

type fakeCountryReader struct {
	country string
	closed  bool
}

func (r *fakeCountryReader) Lookup(net.IP) (string, error) { return r.country, nil }
func (r *fakeCountryReader) Close() error                  { r.closed = true; return nil }

func TestLookupAndAvailability(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mgr.readers.Store(&readerSet{asn: &fakeASNReader{asn: 13335, org: "Cloudflare"}, country: &fakeCountryReader{country: "US"}})
	got := mgr.Lookup(net.ParseIP("1.1.1.1"))
	if got.ASN != 13335 || got.ASOrg != "Cloudflare" || got.Country != "US" {
		t.Fatalf("unexpected lookup: %+v", got)
	}
	if got := mgr.Availability(); !got.ASNDBAvailable || !got.CountryAvailable {
		t.Fatalf("unexpected availability: %+v", got)
	}
}

func TestReloadReopensLocalReadersAtomically(t *testing.T) {
	dir := t.TempDir()
	asnPath := filepath.Join(dir, "asn.mmdb")
	countryPath := filepath.Join(dir, "country.mmdb")
	if err := os.WriteFile(asnPath, []byte("asn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(countryPath, []byte("country"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(config.GeoIPConfig{ASNDBPath: asnPath, CountryDBPath: countryPath}, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldASN := &fakeASNReader{asn: 1}
	oldCountry := &fakeCountryReader{country: "OLD"}
	mgr.readers.Store(&readerSet{asn: oldASN, country: oldCountry})
	mgr.openASN = func(string) (asnReader, error) { return &fakeASNReader{asn: 2}, nil }
	mgr.openCountry = func(string) (countryReader, error) { return &fakeCountryReader{country: "NEW"}, nil }
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := mgr.Lookup(net.ParseIP("1.1.1.1"))
	if got.ASN != 2 || got.Country != "NEW" {
		t.Fatalf("unexpected reloaded lookup: %+v", got)
	}
	if !oldASN.closed || !oldCountry.closed {
		t.Fatal("expected old readers to be closed after swap")
	}
}

func TestReloadHonorsContextAndMissingDatabases(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mgr.Reload(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestStatusReportsLocalFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asn.mmdb")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(config.GeoIPConfig{Enabled: true, ASNDBPath: path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	status := mgr.Status()
	if !status.ASNDB.Configured || status.ASNDB.Path != path {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
}

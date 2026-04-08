package geoip

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
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

func TestStatusReportsConfiguredFilesAndAvailability(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	asnPath := filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb")
	if err := os.WriteFile(asnPath, []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	modTime := time.Unix(1712505600, 0)
	if err := os.Chtimes(asnPath, modTime, modTime); err != nil {
		t.Fatalf("Chtimes error: %v", err)
	}
	mgr.cfg = config.GeoIPConfig{
		Enabled:         true,
		ASNDBURL:        "https://example.test/GeoLite2-ASN.mmdb",
		ASNDBPath:       asnPath,
		RefreshInterval: config.Duration(24 * time.Hour),
	}
	mgr.readers.Store(&readerSet{asn: &fakeASNReader{asn: 13335, asOrg: "Cloudflare"}})

	status := mgr.Status()
	if !status.ASNDB.Configured || !status.ASNDB.Available {
		t.Fatalf("expected configured and available ASN DB: %+v", status)
	}
	if status.ASNDB.Path != asnPath {
		t.Fatalf("unexpected ASN DB path: %+v", status)
	}
	if status.ASNDB.FileModTime != modTime.Unix() {
		t.Fatalf("unexpected mod time: %+v", status)
	}
	if status.ASNDB.FileSize == 0 {
		t.Fatalf("expected file size to be reported: %+v", status)
	}
	if status.CountryDB.Configured || status.CountryDB.Available {
		t.Fatalf("did not expect country DB to be configured: %+v", status)
	}
	if status.RefreshInterval != "24h0m0s" {
		t.Fatalf("unexpected refresh interval: %+v", status)
	}
}

func TestStatusReportsMissingFileAndUnavailableReaderSeparately(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	asnPath := filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb")
	if err := os.WriteFile(asnPath, []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	mgr.cfg = config.GeoIPConfig{
		Enabled:       true,
		ASNDBURL:      "https://example.test/GeoLite2-ASN.mmdb",
		ASNDBPath:     asnPath,
		CountryDBURL:  "https://example.test/Country.mmdb",
		CountryDBPath: filepath.Join(t.TempDir(), "Country.mmdb"),
	}
	mgr.readers.Store(&readerSet{asn: &fakeASNReader{asn: 1}})

	if err := os.Remove(asnPath); err != nil {
		t.Fatalf("Remove error: %v", err)
	}
	status := mgr.Status()
	if !status.ASNDB.Available || status.ASNDB.FileModTime != 0 || status.ASNDB.FileSize != 0 {
		t.Fatalf("expected loaded reader with missing file to report zero file metadata: %+v", status)
	}
	if status.CountryDB.Available || status.CountryDB.FileModTime != 0 || status.CountryDB.FileSize != 0 {
		t.Fatalf("expected unavailable country reader with missing file metadata: %+v", status)
	}
}

func TestStatusReportsUnconfiguredManager(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	status := mgr.Status()
	if status.ASNDB.Configured || status.CountryDB.Configured || status.ASNDB.Available || status.CountryDB.Available {
		t.Fatalf("expected zeroed status for unconfigured manager: %+v", status)
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

func TestRefreshCanPartiallySucceed(t *testing.T) {
	cfg := config.GeoIPConfig{
		Enabled:         true,
		ASNDBURL:        "https://example.test/GeoLite2-ASN.mmdb",
		ASNDBPath:       filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb"),
		CountryDBURL:    "https://example.test/Country.mmdb",
		CountryDBPath:   filepath.Join(t.TempDir(), "Country.mmdb"),
		RefreshInterval: config.Duration(24 * time.Hour),
	}
	mgr, err := NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	oldASN := &fakeASNReader{asn: 1, asOrg: "old-asn"}
	oldCountry := &fakeCountryReader{country: "CA"}
	newCountry := &fakeCountryReader{country: "US"}
	mgr.readers.Store(&readerSet{asn: oldASN, country: oldCountry})
	mgr.download = func(_ context.Context, url, _ string) error {
		if url == cfg.ASNDBURL {
			return errors.New("asn download failed")
		}
		return nil
	}
	mgr.openCountry = func(string) (countryReader, error) { return newCountry, nil }

	mgr.refreshConfigured(context.Background())

	result := mgr.Lookup(net.ParseIP("1.1.1.1"))
	if result.ASN != 1 || result.Country != "US" {
		t.Fatalf("expected mixed old/new readers after partial success, got %+v", result)
	}
	if !oldCountry.closed {
		t.Fatalf("expected old country reader to be closed after swap")
	}
	if oldASN.closed {
		t.Fatalf("did not expect old ASN reader to be closed when refresh failed")
	}
}

func TestRefreshNowWithoutConfiguredDBsFails(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	if _, err := mgr.RefreshNow(context.Background()); !errors.Is(err, ErrNoConfiguredDatabases) {
		t.Fatalf("expected ErrNoConfiguredDatabases, got %v", err)
	}
}

func TestRefreshNowReturnsPerDBResults(t *testing.T) {
	cfg := config.GeoIPConfig{
		Enabled:         true,
		ASNDBURL:        "https://example.test/GeoLite2-ASN.mmdb",
		ASNDBPath:       filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb"),
		CountryDBURL:    "https://example.test/Country.mmdb",
		CountryDBPath:   filepath.Join(t.TempDir(), "Country.mmdb"),
		RefreshInterval: config.Duration(24 * time.Hour),
	}
	mgr, err := NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	oldASN := &fakeASNReader{asn: 1, asOrg: "old"}
	oldCountry := &fakeCountryReader{country: "CA"}
	newASN := &fakeASNReader{asn: 2, asOrg: "new"}
	mgr.readers.Store(&readerSet{asn: oldASN, country: oldCountry})
	mgr.download = func(_ context.Context, url, target string) error {
		return os.WriteFile(target, []byte(url), 0o600)
	}
	mgr.openASN = func(string) (asnReader, error) { return newASN, nil }
	mgr.openCountry = func(string) (countryReader, error) { return nil, errors.New("country open failed") }

	result, err := mgr.RefreshNow(context.Background())
	if err != nil {
		t.Fatalf("RefreshNow error: %v", err)
	}
	if !result.ASNDB.Attempted || !result.ASNDB.Refreshed || result.ASNDB.Error != "" {
		t.Fatalf("expected ASN refresh success, got %+v", result)
	}
	if !result.CountryDB.Attempted || result.CountryDB.Refreshed || result.CountryDB.Error == "" {
		t.Fatalf("expected country refresh failure, got %+v", result)
	}
	lookup := mgr.Lookup(net.ParseIP("1.1.1.1"))
	if lookup.ASN != 2 || lookup.Country != "CA" {
		t.Fatalf("expected mixed active readers after partial refresh, got %+v", lookup)
	}
}

func TestRefreshNowSuccessfulForBothDatabases(t *testing.T) {
	cfg := config.GeoIPConfig{
		Enabled:         true,
		ASNDBURL:        "https://example.test/GeoLite2-ASN.mmdb",
		ASNDBPath:       filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb"),
		CountryDBURL:    "https://example.test/Country.mmdb",
		CountryDBPath:   filepath.Join(t.TempDir(), "Country.mmdb"),
		RefreshInterval: config.Duration(24 * time.Hour),
	}
	mgr, err := NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	mgr.download = func(_ context.Context, url, target string) error {
		return os.WriteFile(target, []byte(url), 0o600)
	}
	mgr.openASN = func(string) (asnReader, error) { return &fakeASNReader{asn: 2, asOrg: "new"}, nil }
	mgr.openCountry = func(string) (countryReader, error) { return &fakeCountryReader{country: "US"}, nil }

	result, err := mgr.RefreshNow(context.Background())
	if err != nil {
		t.Fatalf("RefreshNow error: %v", err)
	}
	if !result.ASNDB.Refreshed || !result.CountryDB.Refreshed {
		t.Fatalf("expected both DBs to refresh, got %+v", result)
	}
	if result.ASNDB.CurrentModTime == 0 || result.CountryDB.CurrentModTime == 0 {
		t.Fatalf("expected mod times after refresh, got %+v", result)
	}
}

func TestRefreshNowSkipsUnconfiguredDatabase(t *testing.T) {
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
	mgr.download = func(_ context.Context, url, target string) error {
		return os.WriteFile(target, []byte(url), 0o600)
	}
	mgr.openASN = func(string) (asnReader, error) { return &fakeASNReader{asn: 2, asOrg: "new"}, nil }

	result, err := mgr.RefreshNow(context.Background())
	if err != nil {
		t.Fatalf("RefreshNow error: %v", err)
	}
	if !result.ASNDB.Attempted || !result.ASNDB.Refreshed {
		t.Fatalf("expected configured ASN DB to refresh, got %+v", result)
	}
	if result.CountryDB.Configured || result.CountryDB.Attempted || result.CountryDB.Refreshed {
		t.Fatalf("expected unconfigured country DB to be skipped, got %+v", result)
	}
}

func TestRefreshNowReportsMissingPreviousFile(t *testing.T) {
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
	mgr.download = func(_ context.Context, url, target string) error {
		return os.WriteFile(target, []byte(url), 0o600)
	}
	mgr.openASN = func(string) (asnReader, error) { return &fakeASNReader{asn: 2}, nil }

	result, err := mgr.RefreshNow(context.Background())
	if err != nil {
		t.Fatalf("RefreshNow error: %v", err)
	}
	if result.ASNDB.PreviousModTime != 0 || result.ASNDB.CurrentModTime == 0 {
		t.Fatalf("expected previous_mod_time=0 and populated current_mod_time, got %+v", result)
	}
}

func TestRefreshNowSerializesConcurrentCalls(t *testing.T) {
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
	block := make(chan struct{})
	var calls int
	var mu sync.Mutex
	mgr.download = func(_ context.Context, url, target string) error {
		mu.Lock()
		calls++
		mu.Unlock()
		<-block
		return os.WriteFile(target, []byte(url), 0o600)
	}
	mgr.openASN = func(string) (asnReader, error) { return &fakeASNReader{asn: 2}, nil }

	var wg sync.WaitGroup
	wg.Add(2)
	errCh := make(chan error, 2)
	go func() {
		defer wg.Done()
		_, err := mgr.RefreshNow(context.Background())
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		_, err := mgr.RefreshNow(context.Background())
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	close(block)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("expected serialized refreshes to succeed, got %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected both refresh calls to run serially, got %d download calls", calls)
	}
}

func TestLookupDuringRefreshSeesConsistentValues(t *testing.T) {
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
	mgr.openASN = func(string) (asnReader, error) {
		time.Sleep(10 * time.Millisecond)
		return newReader, nil
	}

	results := make(chan int, 64)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				asn := mgr.Lookup(net.ParseIP("1.1.1.1")).ASN
				select {
				case results <- asn:
				default:
				}
			}
		}
	}()

	mgr.refreshConfigured(context.Background())
	close(done)
	wg.Wait()
	close(results)

	for asn := range results {
		if asn != 1 && asn != 2 {
			t.Fatalf("lookup observed inconsistent ASN value %d", asn)
		}
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	oldASN := &fakeASNReader{asn: 1}
	oldCountry := &fakeCountryReader{country: "US"}
	mgr.readers.Store(&readerSet{asn: oldASN, country: oldCountry})

	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close error: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close error: %v", err)
	}
	if !oldASN.closed || !oldCountry.closed {
		t.Fatalf("expected readers to be closed on Close")
	}
	availability := mgr.Availability()
	if availability.ASNDBAvailable || availability.CountryAvailable {
		t.Fatalf("expected availability to be cleared after Close: %+v", availability)
	}
}

func TestLoadLocalReadersWithSingleConfiguredDB(t *testing.T) {
	mgr, err := NewManager(config.GeoIPConfig{}, nil)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	path := filepath.Join(t.TempDir(), "Country.mmdb")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	mgr.cfg.CountryDBPath = path
	mgr.openCountry = func(string) (countryReader, error) {
		return &fakeCountryReader{country: "US"}, nil
	}

	mgr.loadLocalReaders()

	availability := mgr.Availability()
	if availability.ASNDBAvailable {
		t.Fatalf("did not expect ASN DB availability")
	}
	if !availability.CountryAvailable {
		t.Fatalf("expected country DB availability")
	}
}

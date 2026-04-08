package geoip

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/oschwald/maxminddb-golang"
)

type LookupResult struct {
	ASN              int    `json:"asn"`
	ASOrg            string `json:"as_org"`
	Country          string `json:"country"`
	ASNDBAvailable   bool   `json:"asn_db_available"`
	CountryAvailable bool   `json:"country_db_available"`
}

type Availability struct {
	ASNDBAvailable   bool
	CountryAvailable bool
}

type LookupProvider interface {
	Lookup(ip net.IP) LookupResult
	Availability() Availability
}

type asnReader interface {
	Lookup(net.IP) (int, string, error)
	Close() error
}

type countryReader interface {
	Lookup(net.IP) (string, error)
	Close() error
}

type readerSet struct {
	asn     asnReader
	country countryReader
}

type Manager struct {
	cfg         config.GeoIPConfig
	logger      util.Logger
	readers     atomic.Value
	openASN     func(string) (asnReader, error)
	openCountry func(string) (countryReader, error)
	download    func(context.Context, string, string) error
	now         func() time.Time
	startOnce   sync.Once
	refreshMu   sync.Mutex
}

type mmdbASNRecord struct {
	ASN   uint   `maxminddb:"autonomous_system_number"`
	ASOrg string `maxminddb:"autonomous_system_organization"`
}

type mmdbCountryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

type mmdbASNReader struct {
	reader *maxminddb.Reader
}

type mmdbCountryReader struct {
	reader *maxminddb.Reader
}

func NewManager(cfg config.GeoIPConfig, logger util.Logger) (*Manager, error) {
	mgr := &Manager{
		cfg:         cfg,
		logger:      util.ComponentLogger(logger, util.CompGeoIP),
		openASN:     openMMDBASNReader,
		openCountry: openMMDBCountryReader,
		download:    downloadFile,
		now:         time.Now,
	}
	mgr.readers.Store(&readerSet{})
	mgr.loadLocalReaders()
	return mgr, nil
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.startOnce.Do(func() {
		m.refreshConfigured(ctx)
		if !m.hasConfiguredDB() {
			return
		}
		ticker := time.NewTicker(m.cfg.RefreshInterval.Duration())
		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					m.refreshConfigured(ctx)
				}
			}
		}()
	})
}

func (m *Manager) Lookup(ip net.IP) LookupResult {
	if m == nil || ip == nil {
		return LookupResult{}
	}
	snapshot := m.snapshot()
	result := LookupResult{
		ASNDBAvailable:   snapshot.asn != nil,
		CountryAvailable: snapshot.country != nil,
	}
	if snapshot.asn != nil {
		asn, org, err := snapshot.asn.Lookup(ip)
		if err == nil {
			result.ASN = asn
			result.ASOrg = org
		}
	}
	if snapshot.country != nil {
		country, err := snapshot.country.Lookup(ip)
		if err == nil {
			result.Country = country
		}
	}
	return result
}

func (m *Manager) Availability() Availability {
	if m == nil {
		return Availability{}
	}
	snapshot := m.snapshot()
	return Availability{
		ASNDBAvailable:   snapshot.asn != nil,
		CountryAvailable: snapshot.country != nil,
	}
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	return m.swapReaders(nil, nil)
}

func (m *Manager) snapshot() *readerSet {
	if m == nil {
		return &readerSet{}
	}
	snapshot, ok := m.readers.Load().(*readerSet)
	if !ok || snapshot == nil {
		return &readerSet{}
	}
	return snapshot
}

func (m *Manager) loadLocalReaders() {
	var asn asnReader
	var country countryReader

	if m.cfg.ASNDBPath != "" {
		reader, err := openASNIfExists(m.cfg.ASNDBPath, m.openASN)
		if err != nil {
			util.Event(m.logger, slog.LevelWarn, "geoip.asn_open_failed", "path", m.cfg.ASNDBPath, "error", err)
		} else {
			asn = reader
		}
	}
	if m.cfg.CountryDBPath != "" {
		reader, err := openCountryIfExists(m.cfg.CountryDBPath, m.openCountry)
		if err != nil {
			util.Event(m.logger, slog.LevelWarn, "geoip.country_open_failed", "path", m.cfg.CountryDBPath, "error", err)
		} else {
			country = reader
		}
	}
	_ = m.swapReaders(asn, country)
	m.logAvailability("geoip.startup_availability")
}

func openASNIfExists(path string, opener func(string) (asnReader, error)) (asnReader, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return opener(path)
}

func openCountryIfExists(path string, opener func(string) (countryReader, error)) (countryReader, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return opener(path)
}

func (m *Manager) refreshConfigured(ctx context.Context) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	if geoDBConfigured(m.cfg.ASNDBURL, m.cfg.ASNDBPath) && m.needsRefresh(m.cfg.ASNDBPath) {
		if err := m.refreshASN(ctx); err != nil {
			util.Event(m.logger, slog.LevelWarn, "geoip.asn_refresh_failed", "path", m.cfg.ASNDBPath, "error", err)
		}
	}
	if geoDBConfigured(m.cfg.CountryDBURL, m.cfg.CountryDBPath) && m.needsRefresh(m.cfg.CountryDBPath) {
		if err := m.refreshCountry(ctx); err != nil {
			util.Event(m.logger, slog.LevelWarn, "geoip.country_refresh_failed", "path", m.cfg.CountryDBPath, "error", err)
		}
	}
}

func (m *Manager) refreshASN(ctx context.Context) error {
	if err := m.download(ctx, m.cfg.ASNDBURL, m.cfg.ASNDBPath); err != nil {
		return err
	}
	reader, err := m.openASN(m.cfg.ASNDBPath)
	if err != nil {
		return err
	}
	snapshot := m.snapshot()
	if err := m.swapReaders(reader, snapshot.country); err != nil {
		_ = reader.Close()
		return err
	}
	util.Event(m.logger, slog.LevelInfo, "geoip.asn_refreshed", "path", m.cfg.ASNDBPath)
	m.logAvailability("geoip.refresh_availability")
	return nil
}

func (m *Manager) refreshCountry(ctx context.Context) error {
	if err := m.download(ctx, m.cfg.CountryDBURL, m.cfg.CountryDBPath); err != nil {
		return err
	}
	reader, err := m.openCountry(m.cfg.CountryDBPath)
	if err != nil {
		return err
	}
	snapshot := m.snapshot()
	if err := m.swapReaders(snapshot.asn, reader); err != nil {
		_ = reader.Close()
		return err
	}
	util.Event(m.logger, slog.LevelInfo, "geoip.country_refreshed", "path", m.cfg.CountryDBPath)
	m.logAvailability("geoip.refresh_availability")
	return nil
}

func (m *Manager) swapReaders(asn asnReader, country countryReader) error {
	oldSnapshot := m.snapshot()
	m.readers.Store(&readerSet{asn: asn, country: country})
	if oldSnapshot.asn != nil && oldSnapshot.asn != asn {
		_ = oldSnapshot.asn.Close()
	}
	if oldSnapshot.country != nil && oldSnapshot.country != country {
		_ = oldSnapshot.country.Close()
	}
	return nil
}

func (m *Manager) needsRefresh(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return m.now().Sub(info.ModTime()) >= m.cfg.RefreshInterval.Duration()
}

func (m *Manager) hasConfiguredDB() bool {
	return geoDBConfigured(m.cfg.ASNDBURL, m.cfg.ASNDBPath) || geoDBConfigured(m.cfg.CountryDBURL, m.cfg.CountryDBPath)
}

func (m *Manager) logAvailability(eventName string) {
	availability := m.Availability()
	util.Event(m.logger, slog.LevelInfo, eventName,
		"geoip.asn_available", availability.ASNDBAvailable,
		"geoip.country_available", availability.CountryAvailable,
	)
}

func geoDBConfigured(url, path string) bool {
	return url != "" && path != ""
}

func openMMDBASNReader(path string) (asnReader, error) {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &mmdbASNReader{reader: reader}, nil
}

func openMMDBCountryReader(path string) (countryReader, error) {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &mmdbCountryReader{reader: reader}, nil
}

func (r *mmdbASNReader) Lookup(ip net.IP) (int, string, error) {
	var record mmdbASNRecord
	if err := r.reader.Lookup(ip, &record); err != nil {
		return 0, "", err
	}
	return int(record.ASN), record.ASOrg, nil
}

func (r *mmdbASNReader) Close() error {
	if r == nil || r.reader == nil {
		return nil
	}
	return r.reader.Close()
}

func (r *mmdbCountryReader) Lookup(ip net.IP) (string, error) {
	var record mmdbCountryRecord
	if err := r.reader.Lookup(ip, &record); err != nil {
		return "", err
	}
	return record.Country.ISOCode, nil
}

func (r *mmdbCountryReader) Close() error {
	if r == nil || r.reader == nil {
		return nil
	}
	return r.reader.Close()
}

func downloadFile(ctx context.Context, sourceURL, targetPath string) error {
	if sourceURL == "" || targetPath == "" {
		return errors.New("missing geoip download url or path")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New(resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(targetPath), filepath.Base(targetPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, targetPath)
}

package geoip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"

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

type DBStatus struct {
	Configured  bool   `json:"configured"`
	Available   bool   `json:"available"`
	Path        string `json:"path"`
	FileModTime int64  `json:"file_mod_time"`
	FileSize    int64  `json:"file_size"`
}

type Status struct {
	ASNDB           DBStatus `json:"asn_db"`
	CountryDB       DBStatus `json:"country_db"`
	RefreshInterval string   `json:"refresh_interval"`
}

type RefreshDBResult struct {
	Configured      bool   `json:"configured"`
	Attempted       bool   `json:"attempted"`
	Refreshed       bool   `json:"refreshed"`
	PreviousModTime int64  `json:"previous_mod_time"`
	CurrentModTime  int64  `json:"current_mod_time"`
	Error           string `json:"error"`
}

type RefreshResult struct {
	ASNDB     RefreshDBResult `json:"asn_db"`
	CountryDB RefreshDBResult `json:"country_db"`
}

var ErrNoConfiguredDatabases = errors.New("no geoip databases configured")

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
	}
	mgr.readers.Store(&readerSet{})
	mgr.loadLocalReaders()
	return mgr, nil
}

func (m *Manager) Start(ctx context.Context) {
	// Database updates are performed out of process. Runtime only loads local
	// files at startup; callers use Reload to pick up an atomic replacement.
}

// Reload reopens the configured local MMDB files and atomically swaps readers.
// It never downloads or writes files.
func (m *Manager) Reload(ctx context.Context) error {
	if m == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	var asn asnReader
	var country countryReader
	var err error
	if m.cfg.ASNDBPath != "" {
		asn, err = m.openASN(m.cfg.ASNDBPath)
		if err != nil {
			return fmt.Errorf("open ASN database: %w", err)
		}
	}
	if m.cfg.CountryDBPath != "" {
		country, err = m.openCountry(m.cfg.CountryDBPath)
		if err != nil {
			if asn != nil {
				_ = asn.Close()
			}
			return fmt.Errorf("open country database: %w", err)
		}
	}
	return m.swapReaders(asn, country)
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

func (m *Manager) Status() Status {
	if m == nil {
		return Status{}
	}
	snapshot := m.snapshot()
	return Status{
		ASNDB:           buildDBStatus(m.cfg.ASNDBURL, m.cfg.ASNDBPath, snapshot.asn != nil),
		CountryDB:       buildDBStatus(m.cfg.CountryDBURL, m.cfg.CountryDBPath, snapshot.country != nil),
		RefreshInterval: m.cfg.RefreshInterval.Duration().String(),
	}
}

func (m *Manager) RefreshNow(ctx context.Context) (RefreshResult, error) {
	if err := m.Reload(ctx); err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{}, nil
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

func (m *Manager) logAvailability(eventName string) {
	availability := m.Availability()
	util.Event(m.logger, slog.LevelInfo, eventName,
		"geoip.asn_available", availability.ASNDBAvailable,
		"geoip.country_available", availability.CountryAvailable,
	)
}

func geoDBConfigured(url, path string) bool {
	return path != ""
}

func buildDBStatus(url, path string, available bool) DBStatus {
	modTime, size := fileStat(path)
	return DBStatus{
		Configured:  geoDBConfigured(url, path),
		Available:   available,
		Path:        path,
		FileModTime: modTime,
		FileSize:    size,
	}
}

func fileStat(path string) (int64, int64) {
	if path == "" {
		return 0, 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return info.ModTime().Unix(), info.Size()
}

func fileModTime(path string) int64 {
	modTime, _ := fileStat(path)
	return modTime
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

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
	if m == nil {
		return RefreshResult{}, nil
	}
	return m.refresh(ctx, true)
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
	_, _ = m.refresh(ctx, false)
}

func (m *Manager) refresh(ctx context.Context, force bool) (RefreshResult, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	if !m.hasConfiguredDB() {
		if force {
			return RefreshResult{}, ErrNoConfiguredDatabases
		}
		return RefreshResult{}, nil
	}

	result := RefreshResult{
		ASNDB:     m.refreshASNResult(ctx, force),
		CountryDB: m.refreshCountryResult(ctx, force),
	}
	return result, nil
}

func (m *Manager) refreshASNResult(ctx context.Context, force bool) RefreshDBResult {
	return m.refreshDB(
		ctx,
		geoDBConfigured(m.cfg.ASNDBURL, m.cfg.ASNDBPath),
		m.cfg.ASNDBURL,
		m.cfg.ASNDBPath,
		force,
		func(path string) (io.Closer, error) {
			return m.openASN(path)
		},
		func(reader io.Closer) error {
			asn, ok := reader.(asnReader)
			if !ok {
				return errors.New("unexpected ASN reader type")
			}
			snapshot := m.snapshot()
			return m.swapReaders(asn, snapshot.country)
		},
		"geoip.asn_refreshed",
		"geoip.asn_refresh_failed",
	)
}

func (m *Manager) refreshCountryResult(ctx context.Context, force bool) RefreshDBResult {
	return m.refreshDB(
		ctx,
		geoDBConfigured(m.cfg.CountryDBURL, m.cfg.CountryDBPath),
		m.cfg.CountryDBURL,
		m.cfg.CountryDBPath,
		force,
		func(path string) (io.Closer, error) {
			return m.openCountry(path)
		},
		func(reader io.Closer) error {
			country, ok := reader.(countryReader)
			if !ok {
				return errors.New("unexpected country reader type")
			}
			snapshot := m.snapshot()
			return m.swapReaders(snapshot.asn, country)
		},
		"geoip.country_refreshed",
		"geoip.country_refresh_failed",
	)
}

func (m *Manager) refreshDB(ctx context.Context, configured bool, sourceURL, targetPath string, force bool, open func(string) (io.Closer, error), activate func(io.Closer) error, successEvent string, failureEvent string) RefreshDBResult {
	result := RefreshDBResult{
		Configured:      configured,
		PreviousModTime: fileModTime(targetPath),
		CurrentModTime:  fileModTime(targetPath),
	}
	if !configured {
		return result
	}
	if !force && !m.needsRefresh(targetPath) {
		return result
	}
	result.Attempted = true

	tmpPath, err := downloadTempPath(ctx, sourceURL, targetPath, m.download)
	if err != nil {
		result.Error = err.Error()
		result.CurrentModTime = fileModTime(targetPath)
		util.Event(m.logger, slog.LevelWarn, failureEvent, "path", targetPath, "error", err)
		return result
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	reader, err := open(tmpPath)
	if err != nil {
		result.Error = err.Error()
		result.CurrentModTime = fileModTime(targetPath)
		util.Event(m.logger, slog.LevelWarn, failureEvent, "path", targetPath, "error", err)
		return result
	}
	defer func() {
		if !result.Refreshed {
			_ = reader.Close()
		}
	}()
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		result.Error = err.Error()
		result.CurrentModTime = fileModTime(targetPath)
		util.Event(m.logger, slog.LevelWarn, failureEvent, "path", targetPath, "error", err)
		return result
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		result.Error = err.Error()
		result.CurrentModTime = fileModTime(targetPath)
		util.Event(m.logger, slog.LevelWarn, failureEvent, "path", targetPath, "error", err)
		return result
	}
	if err := activate(reader); err != nil {
		result.Error = err.Error()
		result.CurrentModTime = fileModTime(targetPath)
		util.Event(m.logger, slog.LevelWarn, failureEvent, "path", targetPath, "error", err)
		return result
	}
	result.Refreshed = true
	result.CurrentModTime = fileModTime(targetPath)
	util.Event(m.logger, slog.LevelInfo, successEvent, "path", targetPath)
	m.logAvailability("geoip.refresh_availability")
	return result
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

func downloadTempPath(ctx context.Context, sourceURL, targetPath string, downloader func(context.Context, string, string) error) (string, error) {
	if sourceURL == "" || targetPath == "" {
		return "", errors.New("missing geoip download url or path")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(targetPath), filepath.Base(targetPath)+".manual-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := downloader(ctx, sourceURL, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
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

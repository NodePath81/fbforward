package fbmeasure

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
)

const (
	SecurityModeOff  = "off"
	SecurityModeTLS  = "tls"
	SecurityModeMTLS = "mtls"
)

type ClientSecurityConfig struct {
	Mode           string
	CAFile         string
	ServerName     string
	ClientCertFile string
	ClientKeyFile  string
}

func (c ClientSecurityConfig) normalizedMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	if mode == "" {
		return SecurityModeOff
	}
	return mode
}

func (c ClientSecurityConfig) Enabled() bool {
	return c.normalizedMode() != SecurityModeOff
}

func (c ClientSecurityConfig) TLSConfig(addr string) (*tls.Config, error) {
	mode := c.normalizedMode()
	if mode == SecurityModeOff {
		return nil, nil
	}
	if mode != SecurityModeTLS && mode != SecurityModeMTLS {
		return nil, fmt.Errorf("unsupported security mode %q", c.Mode)
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("parse ca_file: no certificates found")
		}
		tlsCfg.RootCAs = pool
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split host/port: %w", err)
	}
	serverName := strings.TrimSpace(c.ServerName)
	if serverName == "" && net.ParseIP(host) == nil {
		serverName = host
	}
	tlsCfg.ServerName = serverName

	if mode == SecurityModeMTLS {
		if strings.TrimSpace(c.ClientCertFile) == "" || strings.TrimSpace(c.ClientKeyFile) == "" {
			return nil, fmt.Errorf("mtls requires client_cert_file and client_key_file")
		}
		cert, err := tls.LoadX509KeyPair(c.ClientCertFile, c.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}

type ServerSecurityConfig struct {
	CertFile          string
	KeyFile           string
	ClientCAFile      string
	RequireClientCert bool
}

func (c ServerSecurityConfig) Enabled() bool {
	return strings.TrimSpace(c.CertFile) != "" || strings.TrimSpace(c.KeyFile) != "" || strings.TrimSpace(c.ClientCAFile) != "" || c.RequireClientCert
}

func (c ServerSecurityConfig) TLSConfig() (*tls.Config, error) {
	if !c.Enabled() {
		return nil, nil
	}
	if strings.TrimSpace(c.CertFile) == "" || strings.TrimSpace(c.KeyFile) == "" {
		return nil, fmt.Errorf("tls server mode requires cert_file and key_file")
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server certificate: %w", err)
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if c.ClientCAFile != "" {
		pem, err := os.ReadFile(c.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read client_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("parse client_ca_file: no certificates found")
		}
		tlsCfg.ClientCAs = pool
	}
	if c.RequireClientCert {
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
		if tlsCfg.ClientCAs == nil {
			return nil, fmt.Errorf("require_client_cert needs client_ca_file")
		}
	}
	return tlsCfg, nil
}

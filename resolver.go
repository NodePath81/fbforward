package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

type Resolver struct {
	resolver *net.Resolver
	servers  []string
	next     uint32
	strategy string
}

func NewResolver(cfg ResolverConfig) *Resolver {
	strategy := strings.ToLower(strings.TrimSpace(cfg.Strategy))
	if len(cfg.Servers) == 0 {
		return &Resolver{resolver: net.DefaultResolver, strategy: strategy}
	}
	servers := make([]string, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(server); err != nil {
			server = net.JoinHostPort(server, "53")
		}
		servers = append(servers, server)
	}
	r := &Resolver{servers: servers, strategy: strategy}
	r.resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if len(r.servers) == 0 {
				d := net.Dialer{Timeout: 2 * time.Second}
				return d.DialContext(ctx, network, address)
			}
			idx := atomic.AddUint32(&r.next, 1)
			server := r.servers[int(idx)%len(r.servers)]
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, "udp", server)
		},
	}
	return r
}

func (r *Resolver) ResolveHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if r.strategy == resolverStrategyIPv4Only && ip.To4() == nil {
			return nil, fmt.Errorf("ipv6 address not allowed for resolver.strategy=%s", r.strategy)
		}
		return []net.IP{ip}, nil
	}
	if r.resolver == nil {
		return nil, fmt.Errorf("resolver not initialized")
	}
	addrs, err := r.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP != nil {
			ips = append(ips, addr.IP)
		}
	}
	ips = applyResolverStrategy(ips, r.strategy)
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IPs resolved for %s", host)
	}
	return ips, nil
}

func applyResolverStrategy(ips []net.IP, strategy string) []net.IP {
	if len(ips) == 0 {
		return ips
	}
	switch strategy {
	case resolverStrategyIPv4Only:
		return filterIPv4(ips)
	case resolverStrategyPreferV6:
		v6 := filterIPv6(ips)
		if len(v6) > 0 {
			return v6
		}
		return filterIPv4(ips)
	default:
		return ips
	}
}

func filterIPv4(ips []net.IP) []net.IP {
	filtered := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip != nil && ip.To4() != nil {
			filtered = append(filtered, ip)
		}
	}
	return filtered
}

func filterIPv6(ips []net.IP) []net.IP {
	filtered := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip != nil && ip.To4() == nil {
			filtered = append(filtered, ip)
		}
	}
	return filtered
}

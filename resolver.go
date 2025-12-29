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
}

func NewResolver(cfg ResolverConfig) *Resolver {
	if len(cfg.Servers) == 0 {
		return &Resolver{resolver: net.DefaultResolver}
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
	r := &Resolver{servers: servers}
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
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IPs resolved for %s", host)
	}
	return ips, nil
}

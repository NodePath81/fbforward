package flowcontext

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

const maxResolveWaitMS = 5000

// ResolveRequest is the wire-independent request model used by the HTTP
// service. Addresses are parsed before they reach the registry so the
// registry's key comparison is always canonical.
type ResolveRequest struct {
	Protocol   string `json:"protocol"`
	BackendKey string `json:"backend_key"`
	LocalAddr  string `json:"local_addr"`
	RemoteAddr string `json:"remote_addr"`
	WaitMS     int    `json:"wait_ms,omitempty"`
}

// ParseTuple validates and canonicalizes a resolve request.
func ParseTuple(request ResolveRequest) (flow.BackendTuple, time.Duration, error) {
	protocol := strings.ToLower(strings.TrimSpace(request.Protocol))
	if protocol != flow.ProtocolTCP && protocol != flow.ProtocolUDP {
		return flow.BackendTuple{}, 0, ErrInvalidTuple
	}
	backendKey := strings.TrimSpace(request.BackendKey)
	if backendKey == "" {
		return flow.BackendTuple{}, 0, ErrInvalidTuple
	}
	localAddr, err := netip.ParseAddrPort(strings.TrimSpace(request.LocalAddr))
	if err != nil {
		return flow.BackendTuple{}, 0, ErrInvalidTuple
	}
	remoteAddr, err := netip.ParseAddrPort(strings.TrimSpace(request.RemoteAddr))
	if err != nil {
		return flow.BackendTuple{}, 0, ErrInvalidTuple
	}
	if request.WaitMS < 0 || request.WaitMS > maxResolveWaitMS {
		return flow.BackendTuple{}, 0, ErrInvalidTuple
	}
	return flow.BackendTuple{
		Protocol:   protocol,
		BackendKey: backendKey,
		LocalAddr:  localAddr,
		RemoteAddr: remoteAddr,
	}, time.Duration(request.WaitMS) * time.Millisecond, nil
}

// ResolveRequest resolves a textual tuple without coupling callers to the
// registry's internal key representation.
func (r *Registry) ResolveRequest(ctx context.Context, request ResolveRequest) (Context, error) {
	tuple, wait, err := ParseTuple(request)
	if err != nil {
		return Context{}, err
	}
	if wait > r.options.ResolveTimeout {
		return Context{}, ErrInvalidTuple
	}
	result, ok := r.Resolve(ctx, tuple, wait)
	if !ok {
		if r.IsClosed() {
			return Context{}, ErrClosed
		}
		return Context{}, ErrFlowNotFound
	}
	return result, nil
}

func (r *Registry) IsClosed() bool {
	if r == nil {
		return true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.closed
}

package flowcontextclient

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

type flowContextKey struct{}

// ResolveConn converts a backend-side socket into fbforward's socket
// perspective. The backend peer is fbforward's local endpoint.
func (c *Client) ResolveConn(ctx context.Context, conn net.Conn) (Flow, error) {
	if conn == nil || conn.LocalAddr() == nil || conn.RemoteAddr() == nil {
		return Flow{}, ErrInvalidRequest
	}
	local, err := netAddrPort(conn.RemoteAddr())
	if err != nil {
		return Flow{}, fmt.Errorf("%w: local address: %v", ErrInvalidRequest, err)
	}
	remote, err := netAddrPort(conn.LocalAddr())
	if err != nil {
		return Flow{}, fmt.Errorf("%w: remote address: %v", ErrInvalidRequest, err)
	}
	return c.ResolveTuple(ctx, Tuple{Protocol: "tcp", BackendKey: c.backendKey, LocalAddr: local, RemoteAddr: remote})
}

func netAddrPort(address net.Addr) (netip.AddrPort, error) {
	parsed, err := netip.ParseAddrPort(address.String())
	if err != nil {
		return netip.AddrPort{}, err
	}
	return parsed, nil
}

// ConnContext resolves a backend connection before an http.Server begins
// serving requests on it. Resolution failures leave the parent context
// unchanged, so callers can choose their own fail-open or fail-closed policy.
func (s *ClientSet) ConnContext(parent context.Context, conn net.Conn) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	flow, err := s.ResolveConn(parent, conn)
	if err != nil {
		return parent
	}
	return context.WithValue(parent, flowContextKey{}, flow)
}

// FromContext returns the stable Flow fields stored by ConnContext.
func FromContext(ctx context.Context) (Flow, bool) {
	flow, ok := ResolvedFromContext(ctx)
	if !ok {
		return Flow{}, false
	}
	return flow.Flow, true
}

// ResolvedFromContext returns the flow and source instance stored by
// ConnContext. It is useful when a handler needs to write a tag back to the
// same fbforward instance.
func ResolvedFromContext(ctx context.Context) (ResolvedFlow, bool) {
	if ctx == nil {
		return ResolvedFlow{}, false
	}
	flow, ok := ctx.Value(flowContextKey{}).(ResolvedFlow)
	return flow, ok
}

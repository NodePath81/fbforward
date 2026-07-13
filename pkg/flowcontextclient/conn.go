package flowcontextclient

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

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

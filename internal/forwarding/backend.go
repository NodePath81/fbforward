package forwarding

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/NodePath81/fbforward/internal/flow"
)

func backendTuple(protocol, upstream string, local, remote net.Addr) (flow.BackendTuple, error) {
	localAddr, err := netAddrPort(local)
	if err != nil {
		return flow.BackendTuple{}, err
	}
	remoteAddr, err := netAddrPort(remote)
	if err != nil {
		return flow.BackendTuple{}, err
	}
	return flow.BackendTuple{
		Protocol:   protocol,
		BackendKey: fmt.Sprintf("%s@%s", strings.TrimSpace(upstream), remoteAddr.String()),
		LocalAddr:  localAddr,
		RemoteAddr: remoteAddr,
	}, nil
}

func netAddrPort(address net.Addr) (netip.AddrPort, error) {
	if address == nil {
		return netip.AddrPort{}, fmt.Errorf("nil socket address")
	}
	parsed, err := netip.ParseAddrPort(address.String())
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("parse socket address %q: %w", address.String(), err)
	}
	return parsed, nil
}

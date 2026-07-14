package flowcontextclient

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// InstanceOptions describes one fbforward instance as seen by the backend.
// SourceAddr must be the unique address that fbforward uses when connecting
// to this backend.
type InstanceOptions struct {
	Name       string
	SourceAddr netip.Addr
	Client     Options
}

type clientInstance struct {
	name       string
	sourceAddr netip.Addr
	client     *Client
}

// ClientSet is an immutable, small collection of clients selected by source
// address. A linear scan is intentional: deployments have only a few
// fbforward instances and exact address matching is the contract.
type ClientSet struct {
	instances []clientInstance
}

// ResolvedFlow includes the instance that answered the lookup. The private
// client keeps subsequent tag writes on that same fbforward instance.
type ResolvedFlow struct {
	Flow
	Instance string

	client *Client
}

// NewClientSet validates and constructs a static set of fbforward clients.
// Empty sets are allowed so an optional Flow Context integration can simply
// return ErrUnknownInstance for every connection.
func NewClientSet(options []InstanceOptions) (*ClientSet, error) {
	instances := make([]clientInstance, 0, len(options))
	names := make(map[string]struct{}, len(options))
	addresses := make(map[netip.Addr]struct{}, len(options))
	for index, option := range options {
		name := strings.TrimSpace(option.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: instance %d has empty name", ErrInvalidRequest, index)
		}
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("%w: duplicate instance name %q", ErrInvalidRequest, name)
		}
		if !option.SourceAddr.IsValid() || option.SourceAddr.IsUnspecified() {
			return nil, fmt.Errorf("%w: instance %q has invalid source address", ErrInvalidRequest, name)
		}
		if _, exists := addresses[option.SourceAddr]; exists {
			return nil, fmt.Errorf("%w: duplicate source address %s", ErrInvalidRequest, option.SourceAddr)
		}
		client, err := New(option.Client)
		if err != nil {
			return nil, fmt.Errorf("%w: instance %q client: %v", ErrInvalidRequest, name, err)
		}
		names[name] = struct{}{}
		addresses[option.SourceAddr] = struct{}{}
		instances = append(instances, clientInstance{name: name, sourceAddr: option.SourceAddr, client: client})
	}
	return &ClientSet{instances: instances}, nil
}

// HasSource reports whether addr is configured as an fbforward source
// address. It performs only the local immutable set lookup and never contacts
// an endpoint.
func (s *ClientSet) HasSource(addr netip.Addr) bool {
	if s == nil || !addr.IsValid() {
		return false
	}
	for _, instance := range s.instances {
		if instance.sourceAddr == addr {
			return true
		}
	}
	return false
}

// ResolveBackendTuple resolves a tuple expressed from fbforward's socket
// perspective. localAddr is the fbforward source address and selects the
// configured instance; remoteAddr is the MicProxy listener address.
func (s *ClientSet) ResolveBackendTuple(ctx context.Context, protocol string, localAddr, remoteAddr netip.AddrPort) (ResolvedFlow, error) {
	if s == nil || !localAddr.IsValid() || !remoteAddr.IsValid() {
		return ResolvedFlow{}, ErrInvalidRequest
	}
	for _, instance := range s.instances {
		if instance.sourceAddr != localAddr.Addr() {
			continue
		}
		flow, err := instance.client.ResolveTuple(ctx, Tuple{
			Protocol:   protocol,
			BackendKey: instance.client.backendKey,
			LocalAddr:  localAddr,
			RemoteAddr: remoteAddr,
		})
		if err != nil {
			return ResolvedFlow{}, err
		}
		return ResolvedFlow{Flow: flow, Instance: instance.name, client: instance.client}, nil
	}
	return ResolvedFlow{}, ErrUnknownInstance
}

// ResolveConn selects the only configured instance whose source address is
// visible as conn.RemoteAddr(). No other endpoint is queried when the source
// is unknown or the selected endpoint returns an error.
func (s *ClientSet) ResolveConn(ctx context.Context, conn net.Conn) (ResolvedFlow, error) {
	if s == nil || conn == nil || conn.RemoteAddr() == nil {
		return ResolvedFlow{}, ErrUnknownInstance
	}
	remote, err := netAddrPort(conn.RemoteAddr())
	if err != nil {
		return ResolvedFlow{}, fmt.Errorf("%w: source address: %v", ErrInvalidRequest, err)
	}
	if conn.LocalAddr() == nil {
		return ResolvedFlow{}, ErrInvalidRequest
	}
	local, err := netAddrPort(conn.LocalAddr())
	if err != nil {
		return ResolvedFlow{}, fmt.Errorf("%w: remote address: %v", ErrInvalidRequest, err)
	}
	return s.ResolveBackendTuple(ctx, "tcp", remote, local)
}

// SetFlowTag writes a tag to the fbforward instance that resolved the flow.
func (f ResolvedFlow) SetFlowTag(ctx context.Context, tag Tag) error {
	if f.client == nil || f.ID == "" {
		return ErrInvalidRequest
	}
	return f.client.SetFlowTag(ctx, f.ID, tag)
}

// SetClientTag writes a client tag to the fbforward instance that resolved
// the flow.
func (f ResolvedFlow) SetClientTag(ctx context.Context, tag Tag) error {
	if f.client == nil || f.ID == "" {
		return ErrInvalidRequest
	}
	return f.client.SetClientTag(ctx, f.ID, tag)
}

// UnsetFlowTag removes a flow tag from the source instance.
func (f ResolvedFlow) UnsetFlowTag(ctx context.Context, namespace, key string) error {
	if f.client == nil || f.ID == "" {
		return ErrInvalidRequest
	}
	return f.client.UnsetFlowTag(ctx, f.ID, namespace, key)
}

// UnsetClientTag removes a client tag from the source instance.
func (f ResolvedFlow) UnsetClientTag(ctx context.Context, namespace, key string) error {
	if f.client == nil || f.ID == "" {
		return ErrInvalidRequest
	}
	return f.client.UnsetClientTag(ctx, f.ID, namespace, key)
}

func (f ResolvedFlow) SetLimit(ctx context.Context, rateBPS uint64) error {
	if f.client == nil || f.ID == "" {
		return ErrInvalidRequest
	}
	return f.client.SetFlowLimit(ctx, f.ID, rateBPS)
}

func (f ResolvedFlow) ClearLimit(ctx context.Context) error {
	if f.client == nil || f.ID == "" {
		return ErrInvalidRequest
	}
	return f.client.ClearFlowLimit(ctx, f.ID)
}

func (f ResolvedFlow) Block(ctx context.Context, reason string) error {
	if f.client == nil || f.ID == "" {
		return ErrInvalidRequest
	}
	return f.client.BlockFlow(ctx, f.ID, reason)
}

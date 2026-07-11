package forwarding

import (
	"net/netip"
	"testing"

	"github.com/NodePath81/fbforward/internal/flow"
)

type overrideTestPicker struct {
	selected Upstream
	used     *bool
}

func (p overrideTestPicker) Pick(flow.Meta) (Upstream, error) { return p.selected, nil }

func (p overrideTestPicker) PickOverride(_ flow.Meta, tag string) (Upstream, error) {
	if tag != "backup" || p.used == nil {
		return Upstream{}, nil
	}
	*p.used = true
	return p.selected, nil
}

func TestTCPPickUpstreamUsesRouteOverride(t *testing.T) {
	used := false
	listener := &TCPListener{picker: overrideTestPicker{selected: Upstream{Tag: "backup", Addr: netip.MustParseAddr("192.0.2.10")}, used: &used}}
	selected, err := listener.pickUpstream(flow.Meta{Protocol: flow.ProtocolTCP}, Decision{Allowed: true, UpstreamOverride: "backup"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Tag != "backup" || selected.Addr.String() != "192.0.2.10" {
		t.Fatalf("unexpected override upstream: %+v", selected)
	}
	if !used {
		t.Fatal("override picker was not called")
	}
}

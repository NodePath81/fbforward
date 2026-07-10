package forwarding

import (
	"net"
	"testing"

	"github.com/NodePath81/fbforward/internal/flow"
)

func TestBackendTupleUsesSocketPerspective(t *testing.T) {
	tuple, err := backendTuple(
		flow.ProtocolTCP,
		"primary",
		&net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 43122},
		&net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 443},
	)
	if err != nil {
		t.Fatal(err)
	}
	if tuple.Protocol != flow.ProtocolTCP || tuple.BackendKey != "primary@192.0.2.10:443" {
		t.Fatalf("unexpected tuple: %+v", tuple)
	}
	if tuple.LocalAddr.String() != "10.0.0.1:43122" || tuple.RemoteAddr.String() != "192.0.2.10:443" {
		t.Fatalf("unexpected socket addresses: %+v", tuple)
	}
}

func TestBackendTupleRejectsMissingAddress(t *testing.T) {
	if _, err := backendTuple(flow.ProtocolUDP, "primary", nil, &net.UDPAddr{}); err == nil {
		t.Fatal("expected missing address error")
	}
}

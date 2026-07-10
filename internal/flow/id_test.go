package flow

import (
	"strings"
	"testing"
)

func TestNewIDIsOpaqueAndUnique(t *testing.T) {
	first, err := NewID()
	if err != nil {
		t.Fatalf("NewID error: %v", err)
	}
	if first == "" || !strings.HasPrefix(first.String(), "f_") {
		t.Fatalf("unexpected FlowID %q", first)
	}
	seen := map[ID]struct{}{first: {}}
	for i := 0; i < 1000; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID error: %v", err)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate FlowID %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(first.String()) != 24 {
		t.Fatalf("unexpected FlowID length %d for %q", len(first.String()), first)
	}
}

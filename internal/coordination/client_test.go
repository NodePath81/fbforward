package coordination

import "testing"

func TestBuildNodeURLRemovesQueryAndAppendsNodePath(t *testing.T) {
	got, err := buildNodeURL("https://fbcoord.example/workers/base?pool=default")
	if err != nil {
		t.Fatalf("buildNodeURL returned error: %v", err)
	}
	if got != "wss://fbcoord.example/workers/base/ws/node" {
		t.Fatalf("unexpected ws url: %s", got)
	}
}

func TestBuildNodeURLHandlesBareEndpoint(t *testing.T) {
	got, err := buildNodeURL("https://fbcoord.example")
	if err != nil {
		t.Fatalf("buildNodeURL returned error: %v", err)
	}
	if got != "wss://fbcoord.example/ws/node" {
		t.Fatalf("unexpected ws url: %s", got)
	}
}

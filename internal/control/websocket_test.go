package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketSubscribeAndUnsubscribe(t *testing.T) {
	server := newTestControlServer(t)
	httpServer := func() (srv *httptest.Server) {
		defer func() {
			if recover() != nil {
				srv = nil
			}
		}()
		return httptest.NewServer(http.HandlerFunc(server.handleStatus))
	}()
	if httpServer == nil {
		t.Skip("loopback listener unavailable in this environment")
	}
	defer httpServer.Close()

	url := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(url, http.Header{"Authorization": []string{"Bearer 0123456789abcdef"}})
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "interval_ms": 1000}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	seen := map[string]bool{}
	for len(seen) < 2 {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		var message struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatalf("decode message: %v", err)
		}
		seen[message.Type] = true
	}
	if !seen["connections_snapshot"] || !seen["queue_snapshot"] {
		t.Fatalf("missing snapshots: %#v", seen)
	}
	if err := conn.WriteJSON(map[string]any{"type": "unsubscribe"}); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
}

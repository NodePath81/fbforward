package coordination

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/gorilla/websocket"
)

type testCoordServer struct {
	server *httptest.Server

	helloReceived       chan struct{}
	preferencesReceived chan PreferencesMessage
	byeReceived         chan struct{}
	clientClosed        chan struct{}

	mu              sync.Mutex
	sawBye          bool
	closedBeforeBye bool
	sendReady       bool
	sendClosing     bool
}

func newTestCoordServer(t *testing.T, sendReady bool, sendClosing bool) *testCoordServer {
	t.Helper()

	ts := &testCoordServer{
		helloReceived:       make(chan struct{}, 1),
		preferencesReceived: make(chan PreferencesMessage, 4),
		byeReceived:         make(chan struct{}, 1),
		clientClosed:        make(chan struct{}, 1),
		sendReady:           sendReady,
		sendClosing:         sendClosing,
	}

	upgrader := websocket.Upgrader{}
	ts.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/node" {
			http.NotFound(w, r)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		go ts.serveConn(t, conn)
	}))
	t.Cleanup(ts.server.Close)
	return ts
}

func (ts *testCoordServer) serveConn(t *testing.T, conn *websocket.Conn) {
	defer conn.Close()

	type envelope struct {
		Type string `json:"type"`
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Errorf("read hello: %v", err)
		return
	}
	var first envelope
	if err := json.Unmarshal(data, &first); err != nil {
		t.Errorf("decode hello envelope: %v", err)
		return
	}
	if first.Type != "hello" {
		t.Errorf("expected hello, got %q", first.Type)
		return
	}
	ts.helloReceived <- struct{}{}

	if !ts.sendReady {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				ts.recordClientClosed(false)
				return
			}
		}
	}

	if err := conn.WriteJSON(ReadyMessage{Type: "ready", NodeID: "node-test"}); err != nil {
		t.Errorf("write ready: %v", err)
		return
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			ts.recordClientClosed(false)
			return
		}

		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Errorf("decode envelope: %v", err)
			return
		}

		switch env.Type {
		case "preferences":
			var msg PreferencesMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Errorf("decode preferences: %v", err)
				return
			}
			ts.preferencesReceived <- msg
		case "bye":
			ts.recordBye()
			if ts.sendClosing {
				if err := conn.WriteJSON(ClosingMessage{Type: "closing"}); err != nil {
					t.Errorf("write closing: %v", err)
					return
				}
			}
		case "heartbeat":
		default:
			t.Errorf("unexpected message type %q", env.Type)
			return
		}
	}
}

func (ts *testCoordServer) recordBye() {
	ts.mu.Lock()
	ts.sawBye = true
	ts.mu.Unlock()
	ts.byeReceived <- struct{}{}
}

func (ts *testCoordServer) recordClientClosed(beforeBye bool) {
	ts.mu.Lock()
	ts.closedBeforeBye = beforeBye || !ts.sawBye
	ts.mu.Unlock()
	ts.clientClosed <- struct{}{}
}

func (ts *testCoordServer) closedBeforeByeObserved() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.closedBeforeBye
}

func newTestController(t *testing.T, endpoint string) *Controller {
	t.Helper()

	manager := upstream.NewUpstreamManager(nil, rand.New(rand.NewSource(1)), nil)
	manager.SetCoordination()

	return NewController(
		context.Background(),
		config.CoordinationConfig{
			Endpoint:          endpoint,
			Token:             "node-token-for-tests",
			HeartbeatInterval: config.Duration(time.Hour),
		},
		manager,
		nil,
		nil,
	)
}

func waitForSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func waitForPreferences(t *testing.T, ch <-chan PreferencesMessage) PreferencesMessage {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for preferences")
		return PreferencesMessage{}
	}
}

func TestControllerCloseAfterReadySendsByeBeforeSocketClose(t *testing.T) {
	server := newTestCoordServer(t, true, true)
	controller := newTestController(t, server.server.URL)

	controller.Enable()
	waitForPreferences(t, server.preferencesReceived)

	done := make(chan struct{})
	go func() {
		controller.Close()
		close(done)
	}()

	waitForSignal(t, server.byeReceived, "bye")
	waitForSignal(t, server.clientClosed, "client close")

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("controller close did not return")
	}

	if server.closedBeforeByeObserved() {
		t.Fatal("client closed before sending bye")
	}
}

func TestControllerCloseWithoutClosingReturnsAfterTimeout(t *testing.T) {
	server := newTestCoordServer(t, true, false)
	controller := newTestController(t, server.server.URL)

	controller.Enable()
	waitForPreferences(t, server.preferencesReceived)

	start := time.Now()
	controller.Close()
	elapsed := time.Since(start)

	waitForSignal(t, server.byeReceived, "bye")
	waitForSignal(t, server.clientClosed, "client close")

	if elapsed < gracefulTeardownTimeout-(250*time.Millisecond) {
		t.Fatalf("controller close returned too early: %s", elapsed)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("controller close took too long: %s", elapsed)
	}
	if server.closedBeforeByeObserved() {
		t.Fatal("client closed before sending bye")
	}
}

func TestControllerCloseDuringHandshakeClosesSocketPromptly(t *testing.T) {
	server := newTestCoordServer(t, false, false)
	controller := newTestController(t, server.server.URL)

	controller.Enable()
	waitForSignal(t, server.helloReceived, "hello")

	start := time.Now()
	controller.Close()
	elapsed := time.Since(start)

	waitForSignal(t, server.clientClosed, "client close")

	if elapsed > time.Second {
		t.Fatalf("controller close during handshake took too long: %s", elapsed)
	}
}

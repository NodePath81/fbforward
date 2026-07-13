package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientSendsGenericWebhookEvent(t *testing.T) {
	fixedNow := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)

	var gotPath string
	var gotBody string
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotPath = r.URL.Path
		gotBody = string(body)
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Endpoint:       server.URL + "/v1/events",
		BearerToken:    "node-token-abcdefghijklmnopqrstuvwxyz123456",
		SourceInstance: "node-1",
		Now:            func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if !client.Emit("upstream.unusable", SeverityWarn, map[string]any{
		"upstream.tag":    "us-1",
		"upstream.reason": "failover_loss",
	}) {
		t.Fatalf("expected emit to succeed")
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	if gotPath != "/v1/events" {
		t.Fatalf("expected /v1/events path, got %q", gotPath)
	}
	if gotAuth != "Bearer node-token-abcdefghijklmnopqrstuvwxyz123456" {
		t.Fatalf("unexpected authorization header %q", gotAuth)
	}
	if want := `"event":"upstream.unusable"`; !contains(gotBody, want) {
		t.Fatalf("expected body to contain %s, got %s", want, gotBody)
	}
}

func TestClientDropsWhenQueueFull(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	telemetry := &recordingTelemetry{}
	client, err := NewClient(Config{
		Endpoint:       "https://notify.example/v1/events",
		BearerToken:    "node-token-abcdefghijklmnopqrstuvwxyz123456",
		SourceInstance: "node-1",
		QueueSize:      1,
		HTTPClient:     httpClient,
		Telemetry:      telemetry,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if !client.Emit("first", SeverityWarn, nil) {
		t.Fatalf("expected first emit to succeed")
	}
	<-started
	if !client.Emit("second", SeverityWarn, nil) {
		t.Fatalf("expected second emit to succeed")
	}
	if client.Emit("third", SeverityWarn, nil) {
		t.Fatalf("expected third emit to be dropped")
	}

	close(release)
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if telemetry.dropped.Load() != 1 {
		t.Fatalf("expected one dropped event, got %d", telemetry.dropped.Load())
	}
}

func TestClientRetriesServerErrorsAndRecordsSuccess(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	telemetry := &recordingTelemetry{}
	client, err := NewClient(Config{Endpoint: server.URL, Telemetry: telemetry})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	if !client.Emit("retry", SeverityWarn, nil) {
		t.Fatal("expected emit to succeed")
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected three attempts, got %d", got)
	}
	if telemetry.success.Load() != 1 || telemetry.failed.Load() != 0 {
		t.Fatalf("unexpected telemetry: success=%d failed=%d", telemetry.success.Load(), telemetry.failed.Load())
	}
}

func TestClientDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	telemetry := &recordingTelemetry{}
	client, err := NewClient(Config{Endpoint: server.URL, Telemetry: telemetry})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	client.Emit("client-error", SeverityWarn, nil)
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one attempt, got %d", got)
	}
	if telemetry.success.Load() != 0 || telemetry.failed.Load() != 1 {
		t.Fatalf("unexpected telemetry: success=%d failed=%d", telemetry.success.Load(), telemetry.failed.Load())
	}
}

func TestClientRetriesTransportErrors(t *testing.T) {
	var calls atomic.Int32
	telemetry := &recordingTelemetry{}
	client, err := NewClient(Config{
		Endpoint: "https://notify.example/v1/events",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) < 3 {
				return nil, io.ErrUnexpectedEOF
			}
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
		})},
		Telemetry: telemetry,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	client.Emit("transport-retry", SeverityWarn, nil)
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if calls.Load() != 3 || telemetry.success.Load() != 1 || telemetry.failed.Load() != 0 {
		t.Fatalf("unexpected retries or telemetry: calls=%d success=%d failed=%d", calls.Load(), telemetry.success.Load(), telemetry.failed.Load())
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

type recordingTelemetry struct {
	success atomic.Uint64
	failed  atomic.Uint64
	dropped atomic.Uint64
}

func (t *recordingTelemetry) IncWebhookDelivery(result string) {
	if result == "success" {
		t.success.Add(1)
	} else if result == "failed" {
		t.failed.Add(1)
	}
}

func (t *recordingTelemetry) IncWebhookDropped() {
	t.dropped.Add(1)
}

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}

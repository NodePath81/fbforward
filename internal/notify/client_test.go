package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientSignsAndSendsEvent(t *testing.T) {
	fixedNow := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)

	var gotPath string
	var gotBody string
	var gotKeyID string
	var gotTimestamp string
	var gotSignature string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotPath = r.URL.Path
		gotBody = string(body)
		gotKeyID = r.Header.Get("X-FBNotify-Key-Id")
		gotTimestamp = r.Header.Get("X-FBNotify-Timestamp")
		gotSignature = r.Header.Get("X-FBNotify-Signature")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Endpoint:       server.URL + "/v1/events",
		KeyID:          "key-1",
		Token:          "node-token-abcdefghijklmnopqrstuvwxyz123456",
		SourceService:  "fbforward",
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
	if gotKeyID != "key-1" {
		t.Fatalf("unexpected key id %q", gotKeyID)
	}
	if gotTimestamp != "1775822400" {
		t.Fatalf("unexpected timestamp header %q", gotTimestamp)
	}
	wantSignature := rawBase64HMAC(gotTimestamp+"."+gotBody, "node-token-abcdefghijklmnopqrstuvwxyz123456")
	if gotSignature != wantSignature {
		t.Fatalf("unexpected signature %q want %q", gotSignature, wantSignature)
	}
	if want := `"event_name":"upstream.unusable"`; !contains(gotBody, want) {
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

	client, err := NewClient(Config{
		Endpoint:       "https://notify.example/v1/events",
		KeyID:          "key-1",
		Token:          "node-token-abcdefghijklmnopqrstuvwxyz123456",
		SourceService:  "fbforward",
		SourceInstance: "node-1",
		QueueSize:      1,
		HTTPClient:     httpClient,
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
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func rawBase64HMAC(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type forwarderProcess struct {
	baseURL string
	process *exec.Cmd
	cancel  context.CancelFunc
	logs    *processLogs
}

type processLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *processLogs) Write(value []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(value)
}

func (l *processLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type rpcEnvelope struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

func (f *forwarderProcess) rpc(t *testing.T, token, method string, params any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	request, err := http.NewRequest(http.MethodPost, f.baseURL+"/rpc", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatalf("call %s: %v", method, err)
	}
	defer response.Body.Close()
	var envelope rpcEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	if response.StatusCode != http.StatusOK || !envelope.OK {
		t.Fatalf("call %s failed: status=%d error=%q", method, response.StatusCode, envelope.Error)
	}
	return envelope.Result
}

func startForwarder(t *testing.T, config string, controlPort int) *forwarderProcess {
	t.Helper()
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "fbforward")
	build := exec.Command("go", "build", "-o", binary, "./cmd/fbforward")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fbforward: %v\n%s", err, output)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	process := exec.CommandContext(ctx, binary, "run", "--config", configPath)
	process.Dir = root
	logs := &processLogs{}
	process.Stdout = logs
	process.Stderr = logs
	if err := process.Start(); err != nil {
		cancel()
		t.Fatalf("start fbforward: %v", err)
	}
	forwarder := &forwarderProcess{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", controlPort),
		process: process,
		cancel:  cancel,
		logs:    logs,
	}
	t.Cleanup(func() {
		forwarder.cancel()
		_ = forwarder.process.Process.Signal(os.Interrupt)
		_ = forwarder.process.Wait()
		if t.Failed() {
			t.Logf("fbforward process output:\n%s", forwarder.logs.String())
		}
	})
	return forwarder
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	waitForInterval(t, timeout, 25*time.Millisecond, condition)
}

func waitForInterval(t *testing.T, timeout, interval time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatal(strings.TrimSpace("condition did not become true before deadline"))
}

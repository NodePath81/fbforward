//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type forwarderProcess struct {
	baseURL string
	process *exec.Cmd
	cancel  context.CancelFunc
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
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	if err := process.Start(); err != nil {
		cancel()
		t.Fatalf("start fbforward: %v", err)
	}
	forwarder := &forwarderProcess{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", controlPort),
		process: process,
		cancel:  cancel,
	}
	t.Cleanup(func() {
		forwarder.cancel()
		_ = forwarder.process.Process.Signal(os.Interrupt)
		_ = forwarder.process.Wait()
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
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(strings.TrimSpace("condition did not become true before deadline"))
}

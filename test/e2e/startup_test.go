//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStartupServesIdentityAndEmbeddedAssets(t *testing.T) {
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "fbforward")
	build := exec.Command("go", "build", "-o", binary, "./cmd/fbforward")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fbforward: %v\n%s", err, output)
	}

	controlPort := freeTCPPort(t)
	forwardPort := freeTCPPort(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf(`hostname: e2e

listeners:
  - name: e2e
    bind: 127.0.0.1:%d
    protocol: tcp
    route: local

routes:
  - name: local
    strategy: static
    upstreams: [local]

upstreams:
  - tag: local
    destination:
      host: 127.0.0.1

forwarding:
  limits:
    max_tcp_connections: 10
    max_udp_mappings: 10
  idle_timeout:
    tcp: 5s
    udp: 5s

control:
  bind_addr: 127.0.0.1
  bind_port: %d
  auth_token: e2e-control-token
  metrics:
    enabled: true

firewall:
  enabled: false
`, forwardPort, controlPort)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := exec.CommandContext(ctx, binary, "run", "--config", configPath)
	process.Dir = root
	logs, err := process.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	process.Stderr = process.Stdout
	if err := process.Start(); err != nil {
		t.Fatalf("start fbforward: %v", err)
	}
	defer func() {
		cancel()
		_ = process.Process.Signal(os.Interrupt)
		_ = process.Wait()
	}()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	waitFor(t, 5*time.Second, func() bool {
		request, err := http.NewRequest(http.MethodGet, baseURL+"/identity", nil)
		if err != nil {
			return false
		}
		request.Header.Set("Authorization", "Bearer e2e-control-token")
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK
	})

	for _, path := range []string{"/", "/app.js"} {
		response, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		if response.StatusCode != http.StatusOK || len(body) == 0 {
			t.Fatalf("GET %s: status=%d body=%d", path, response.StatusCode, len(body))
		}
	}
	_ = logs
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

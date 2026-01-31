package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Process represents a launched process inside a namespace.
type Process struct {
	Name    string
	Cmd     *exec.Cmd
	LogPath string
}

// ProcessManager tracks running processes.
type ProcessManager struct {
	Processes map[string]*Process
}

// NewProcessManager constructs an empty manager.
func NewProcessManager() *ProcessManager {
	return &ProcessManager{Processes: make(map[string]*Process)}
}

// Start launches a command inside the namespace identified by nsPID.
func (pm *ProcessManager) Start(name string, nsPID int, binary string, args []string, logDir string) error {
	if nsPID == 0 {
		return fmt.Errorf("ns pid not set for %s", name)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", name))
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}

	fullArgs := append([]string{"-t", fmt.Sprint(nsPID), "-n", binary}, args...)
	cmd := exec.Command("nsenter", fullArgs...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return err
	}
	pm.Processes[name] = &Process{Name: name, Cmd: cmd, LogPath: logPath}
	return nil
}

// Stop stops a named process with SIGTERM then SIGKILL after 5s.
func (pm *ProcessManager) Stop(name string) {
	p, ok := pm.Processes[name]
	if !ok || p.Cmd == nil || p.Cmd.Process == nil {
		return
	}
	_ = p.Cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = p.Cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = p.Cmd.Process.Kill()
	}
	delete(pm.Processes, name)
}

// StopAll stops all tracked processes.
func (pm *ProcessManager) StopAll() {
	for name := range pm.Processes {
		pm.Stop(name)
	}
}

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
	Done    chan error
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

	fullArgs := append([]string{"-t", fmt.Sprint(nsPID), "-U", "-n", "--", binary}, args...)
	cmd := exec.Command("nsenter", fullArgs...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	done := make(chan error, 1)
	pm.Processes[name] = &Process{Name: name, Cmd: cmd, LogPath: logPath, Done: done}
	go func() {
		err := cmd.Wait()
		done <- err
		close(done)
	}()
	_ = logFile.Close()
	return nil
}

// Stop stops a named process with SIGTERM then SIGKILL after 5s.
func (pm *ProcessManager) Stop(name string) {
	p, ok := pm.Processes[name]
	if !ok || p.Cmd == nil || p.Cmd.Process == nil {
		return
	}
	select {
	case <-p.Done:
		delete(pm.Processes, name)
		return
	default:
	}
	_ = p.Cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.Done:
	case <-time.After(5 * time.Second):
		_ = p.Cmd.Process.Kill()
		<-p.Done
	}
	delete(pm.Processes, name)
}

// StopAll stops all tracked processes.
func (pm *ProcessManager) StopAll() {
	for name := range pm.Processes {
		pm.Stop(name)
	}
}

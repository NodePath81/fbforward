package harness

import (
	"fmt"
	"os/exec"
)

// Namespace represents a user/network namespace with an associated shell.
type Namespace struct {
	Name     string
	Index    int
	IP       string
	Gateway  string
	VethPair *VethPair

	ShellCmd *exec.Cmd
	ShellPID int
	ParentNS *Namespace
}

// VethPair tracks the two ends of a veth.
type VethPair struct {
	Inner string
	Outer string
}

// Topology holds namespaces for a scenario.
type Topology struct {
	Namespaces map[string]*Namespace
	Internet   *Namespace
}

// LaunchNamespaceShell starts a persistent unshare shell for ns0.
func LaunchNamespaceShell() (*Namespace, error) {
	cmd := exec.Command("unshare", "-Urn", "--kill-child=SIGTERM", "bash", "-c", "echo $$ && sleep infinity")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var pid int
	_, err = fmt.Fscan(stdout, &pid)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("read ns0 pid: %w", err)
	}
	ns := &Namespace{
		Name:     "ns0",
		Index:    0,
		ShellCmd: cmd,
		ShellPID: pid,
	}
	return ns, nil
}

// Cleanup terminates the namespace shell.
func (n *Namespace) Cleanup() error {
	if n == nil || n.ShellCmd == nil || n.ShellCmd.Process == nil {
		return nil
	}
	_ = n.ShellCmd.Process.Kill()
	_, _ = n.ShellCmd.Process.Wait()
	return nil
}

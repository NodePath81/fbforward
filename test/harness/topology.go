package harness

import (
	"fmt"
	"net"
	"os/exec"
	"sort"
)

// Subnet describes a /30 link subnet between hub and a leaf namespace.
type Subnet struct {
	Network  string
	Gateway  string
	Endpoint string
}

// Namespace represents a user/network namespace with an associated shell.
type Namespace struct {
	Name     string
	Index    int
	Tag      string
	Subnet   Subnet
	VethPair *VethPair

	ShellCmd *exec.Cmd
	ShellPID int
	ParentNS *Namespace
}

// VethPair tracks the two ends of a veth.
type VethPair struct {
	Hub  string
	Leaf string
}

// Topology holds namespaces for a scenario.
type Topology struct {
	Name            string
	BaseCIDR        string
	Namespaces      map[string]*Namespace
	Hub             *Namespace
	TrafficSourceNS *Namespace
	ForwarderNS     *Namespace
	UpstreamNS      []*Namespace
	UpstreamByTag   map[string]*Namespace
}

// LaunchNamespaceShell starts a persistent unshare shell for a namespace.
func LaunchNamespaceShell(name string, index int) (*Namespace, error) {
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
		return nil, fmt.Errorf("read namespace pid: %w", err)
	}
	ns := &Namespace{
		Name:     name,
		Index:    index,
		ShellCmd: cmd,
		ShellPID: pid,
	}
	if err := runInNamespace(pid, "ip", "link", "set", "lo", "up"); err != nil {
		_ = ns.Cleanup()
		return nil, fmt.Errorf("enable loopback: %w", err)
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

// CleanupAll terminates all namespace shells in the topology.
func (t *Topology) CleanupAll() {
	if t == nil {
		return
	}
	// Kill leaves first, then hub.
	for _, ns := range t.Namespaces {
		if t.Hub != nil && ns == t.Hub {
			continue
		}
		_ = ns.Cleanup()
	}
	if t.Hub != nil {
		_ = t.Hub.Cleanup()
	}
}

// AllocateSubnets allocates per-link /30 subnets from a base /16.
func AllocateSubnets(baseCIDR string, numLinks int) ([]Subnet, error) {
	ip, ipnet, err := net.ParseCIDR(baseCIDR)
	if err != nil {
		return nil, fmt.Errorf("parse cidr: %w", err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("base CIDR must be IPv4")
	}
	ones, _ := ipnet.Mask.Size()
	if ones > 16 || numLinks > 255 {
		return nil, fmt.Errorf("CIDR %s too small for %d links; need at least /16", baseCIDR, numLinks)
	}
	baseA, baseB := ip4[0], ip4[1]
	subnets := make([]Subnet, 0, numLinks)
	for i := 1; i <= numLinks; i++ {
		netIP := net.IPv4(baseA, baseB, byte(i), 0).To4()
		gw := net.IPv4(baseA, baseB, byte(i), 1).To4()
		ep := net.IPv4(baseA, baseB, byte(i), 2).To4()
		subnets = append(subnets, Subnet{
			Network:  fmt.Sprintf("%s/30", netIP.String()),
			Gateway:  gw.String(),
			Endpoint: ep.String(),
		})
	}
	return subnets, nil
}

// CreateTopology builds the full N+3 namespace topology.
func CreateTopology(name string, baseCIDR string, upstreamTags []string) (*Topology, error) {
	if name == "" {
		name = "scenario"
	}
	upstreamCount := len(upstreamTags)
	numLinks := upstreamCount + 2
	subnets, err := AllocateSubnets(baseCIDR, numLinks)
	if err != nil {
		return nil, err
	}

	hub, err := LaunchNamespaceShell(fmt.Sprintf("%s-0", name), 0)
	if err != nil {
		return nil, err
	}

	topo := &Topology{
		Name:          name,
		BaseCIDR:      baseCIDR,
		Namespaces:    map[string]*Namespace{hub.Name: hub},
		Hub:           hub,
		UpstreamByTag: map[string]*Namespace{},
	}

	// Create leaf namespaces and veth pairs.
	for i := 1; i <= numLinks; i++ {
		leafName := fmt.Sprintf("%s-%d", name, i)
		leaf, err := createChildNamespace(hub, leafName, i)
		if err != nil {
			topo.CleanupAll()
			return nil, err
		}
		subnet := subnets[i-1]
		leaf.Subnet = subnet
		vethName := fmt.Sprintf("veth-%d", i)
		if err := createVethPair(hub, leaf, vethName, subnet); err != nil {
			topo.CleanupAll()
			return nil, err
		}
		topo.Namespaces[leaf.Name] = leaf

		switch i {
		case 1:
			topo.TrafficSourceNS = leaf
		case 2:
			topo.ForwarderNS = leaf
		default:
			topo.UpstreamNS = append(topo.UpstreamNS, leaf)
		}
	}

	// Map upstream tags to namespaces by order.
	sortedTags := append([]string(nil), upstreamTags...)
	sort.Strings(sortedTags)
	for i, tag := range sortedTags {
		if i >= len(topo.UpstreamNS) {
			break
		}
		ns := topo.UpstreamNS[i]
		ns.Tag = tag
		topo.UpstreamByTag[tag] = ns
	}

	if err := setupRouting(hub, topo); err != nil {
		topo.CleanupAll()
		return nil, err
	}

	return topo, nil
}

func createChildNamespace(hub *Namespace, name string, index int) (*Namespace, error) {
	cmd := exec.Command("nsenter", "-t", fmt.Sprint(hub.ShellPID), "-U", "-n", "--",
		"unshare", "-n", "--kill-child=SIGTERM", "bash", "-c", "echo $$ && sleep infinity")
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
		return nil, fmt.Errorf("read child pid: %w", err)
	}
	ns := &Namespace{
		Name:     name,
		Index:    index,
		ShellCmd: cmd,
		ShellPID: pid,
		ParentNS: hub,
	}
	if err := runInNamespace(pid, "ip", "link", "set", "lo", "up"); err != nil {
		_ = ns.Cleanup()
		return nil, fmt.Errorf("enable loopback: %w", err)
	}
	return ns, nil
}

func createVethPair(hub *Namespace, leaf *Namespace, vethName string, subnet Subnet) error {
	hubIface := vethName
	leafIface := vethName + "-peer"

	if err := runInNamespace(hub.ShellPID, "ip", "link", "add", hubIface, "type", "veth", "peer", "name", leafIface); err != nil {
		return err
	}
	if err := runInNamespace(hub.ShellPID, "ip", "link", "set", leafIface, "netns", fmt.Sprintf("/proc/%d/ns/net", leaf.ShellPID)); err != nil {
		return err
	}
	if err := runInNamespace(hub.ShellPID, "ip", "addr", "add", subnet.Gateway+"/30", "dev", hubIface); err != nil {
		return err
	}
	if err := runInNamespace(hub.ShellPID, "ip", "link", "set", hubIface, "up"); err != nil {
		return err
	}

	if err := runInNamespace(leaf.ShellPID, "ip", "link", "set", leafIface, "up"); err != nil {
		return err
	}
	if err := runInNamespace(leaf.ShellPID, "ip", "addr", "add", subnet.Endpoint+"/30", "dev", leafIface); err != nil {
		return err
	}
	if err := runInNamespace(leaf.ShellPID, "ip", "route", "add", "default", "via", subnet.Gateway, "dev", leafIface); err != nil {
		return err
	}

	leaf.VethPair = &VethPair{Hub: hubIface, Leaf: leafIface}
	return nil
}

func setupRouting(hub *Namespace, topo *Topology) error {
	if err := runInNamespace(hub.ShellPID, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	for _, ns := range topo.Namespaces {
		if ns == hub {
			continue
		}
		if ns.VethPair == nil {
			continue
		}
		if err := runInNamespace(hub.ShellPID, "ip", "route", "add", ns.Subnet.Network, "dev", ns.VethPair.Hub); err != nil {
			return err
		}
	}
	return nil
}

func runInNamespace(pid int, args ...string) error {
	cmdArgs := append([]string{"-t", fmt.Sprint(pid), "-U", "-n", "--"}, args...)
	cmd := exec.Command("nsenter", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nsenter %v failed: %w (%s)", args, err, string(output))
	}
	return nil
}

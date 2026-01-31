package harness

import "strconv"

// Iperf3Result represents a parsed iperf3 output sample.
type Iperf3Result struct {
	Stream string
	Bps    float64
}

// StartIperf3Server starts an iperf3 server inside a namespace.
func StartIperf3Server(pm *ProcessManager, name string, nsPID int, port int, logDir string) error {
	args := []string{"-s", "-p", strconv.Itoa(port), "--cntl-ka"}
	return pm.Start(name, nsPID, "iperf3", args, logDir)
}

// StartIperf3Clients starts iperf3 client traffic from a namespace.
func StartIperf3Clients(pm *ProcessManager, name string, nsPID int, target string, port int, durationSec int, parallel int, logDir string) error {
	args := []string{"-c", target, "-p", strconv.Itoa(port), "-t", strconv.Itoa(durationSec)}
	if parallel > 1 {
		args = append(args, "-P", strconv.Itoa(parallel))
	}
	args = append(args, "--cntl-ka")
	return pm.Start(name, nsPID, "iperf3", args, logDir)
}

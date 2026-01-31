package harness

// Iperf3Result represents a parsed iperf3 output sample.
type Iperf3Result struct {
	Stream string
	Bps    float64
}

// StartIperf3Server is a stub for starting an iperf3 server inside a namespace.
func StartIperf3Server(nsPID int, port int) error {
	// Stub: real implementation would run iperf3 -s
	_ = nsPID
	_ = port
	return nil
}

// RunIperf3Client is a stub for running iperf3 client.
func RunIperf3Client(nsPID int, target string, port int, durationSec int) ([]Iperf3Result, error) {
	_ = nsPID
	_ = target
	_ = port
	_ = durationSec
	return nil, nil
}

package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"
	"github.com/NodePath81/fbforward/bwprobe/internal/rpc"
)

type intervalReport struct {
	Bytes      uint64 `json:"bytes"`
	DurationMs int64  `json:"duration_ms"`
	OOOCount   uint64 `json:"ooo_count"`
}

type SampleReport struct {
	SampleID           uint32           `json:"sample_id"`
	TotalBytes         uint64           `json:"total_bytes"`
	TotalDuration      float64          `json:"total_duration"`
	Intervals          []intervalReport `json:"intervals"`
	FirstByteTime      string           `json:"first_byte_time"`
	LastByteTime       string           `json:"last_byte_time"`
	AvgThroughput      float64          `json:"avg_throughput,omitempty"`
	PacketsRecv        uint64           `json:"packets_recv,omitempty"`
	PacketsLost        uint64           `json:"packets_lost,omitempty"`
	TCPSendBufferBytes uint64           `json:"tcp_send_buffer_bytes,omitempty"`
	TCPRetransmits     uint64           `json:"tcp_retransmits,omitempty"`
	TCPSegmentsSent    uint64           `json:"tcp_segments_sent,omitempty"`
}

type controlClient struct {
	conn          net.Conn
	reader        *bufio.Reader
	writer        *bufio.Writer
	rpcClient     *rpc.RPCClient // nil if using legacy protocol
	udpRegistered bool
}

// SessionID returns the session ID if using RPC, empty string otherwise
func (c *controlClient) SessionID() string {
	if c.rpcClient != nil {
		return c.rpcClient.SessionID()
	}
	return ""
}

// RegisterUDP registers the UDP endpoint for reverse mode when using RPC.
func (c *controlClient) RegisterUDP(udpPort int) error {
	if c.rpcClient == nil {
		return nil
	}
	if c.udpRegistered {
		return nil
	}
	if err := c.rpcClient.RegisterUDP(udpPort); err != nil {
		return err
	}
	c.udpRegistered = true
	return nil
}

func (c *controlClient) StartSampleReverse(sampleID uint32, cfg reverseStartConfig) error {
	// Use RPC if available
	if c.rpcClient != nil {
		rttMs := int64(cfg.RTT / time.Millisecond)
		network := "tcp"
		if cfg.UDPPort > 0 {
			network = "udp"
			if !c.udpRegistered {
				// Register UDP endpoint once for reverse mode.
				if err := c.rpcClient.RegisterUDP(cfg.UDPPort); err != nil {
					return err
				}
				c.udpRegistered = true
			}
		}
		return c.rpcClient.StartSampleReverse(sampleID, network, cfg.BandwidthBps, cfg.ChunkSize, rttMs, cfg.SampleBytes)
	}

	// Legacy protocol
	bandwidth := int64(cfg.BandwidthBps)
	rttMs := int64(cfg.RTT / time.Millisecond)
	line := fmt.Sprintf("SAMPLE_START %d REVERSE %d %d %d %d %d",
		sampleID,
		bandwidth,
		cfg.ChunkSize,
		rttMs,
		cfg.SampleBytes,
		cfg.UDPPort)
	if err := c.sendLine(line); err != nil {
		return err
	}
	resp, err := c.readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(resp, "OK") {
		return fmt.Errorf("control start failed: %s", resp)
	}
	return nil
}

// convertRPCReport converts RPC SampleStopResponse to legacy SampleReport
func convertRPCReport(rpcResp rpc.SampleStopResponse) SampleReport {
	intervals := make([]intervalReport, len(rpcResp.Intervals))
	for i, rpcInterval := range rpcResp.Intervals {
		intervals[i] = intervalReport{
			Bytes:      rpcInterval.Bytes,
			DurationMs: rpcInterval.DurationMs,
			OOOCount:   rpcInterval.OOOCount,
		}
	}

	return SampleReport{
		SampleID:           rpcResp.SampleID,
		TotalBytes:         rpcResp.TotalBytes,
		TotalDuration:      rpcResp.TotalDuration,
		Intervals:          intervals,
		FirstByteTime:      rpcResp.FirstByteTime,
		LastByteTime:       rpcResp.LastByteTime,
		AvgThroughput:      rpcResp.AvgThroughputBps,
		PacketsRecv:        rpcResp.PacketsRecv,
		PacketsLost:        rpcResp.PacketsLost,
		TCPSendBufferBytes: rpcResp.TCPSendBufferBytes,
		TCPRetransmits:     rpcResp.TCPRetransmits,
		TCPSegmentsSent:    rpcResp.TCPSegmentsSent,
	}
}

// NewControlClient creates a control connection for sample coordination.
// Tries JSON-RPC protocol first, falls back to legacy text protocol.
func NewControlClient(target string, port int) (*controlClient, error) {
	// Try RPC protocol first
	rpcClient, err := rpc.NewRPCClient(target, port)
	if err == nil {
		return &controlClient{
			rpcClient: rpcClient,
		}, nil
	}

	// Fall back to legacy protocol
	log.Printf("RPC protocol unavailable, using legacy protocol: %v", err)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", target, port), 3*time.Second)
	if err != nil {
		return nil, err
	}

	if _, err := conn.Write([]byte(protocol.TCPControlHeader)); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &controlClient{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
	}, nil
}

func (c *controlClient) Close() error {
	if c == nil {
		return nil
	}
	if c.rpcClient != nil {
		return c.rpcClient.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *controlClient) StartSample(sampleID uint32, network string) error {
	// Use RPC if available
	if c.rpcClient != nil {
		return c.rpcClient.StartSample(sampleID, network)
	}

	// Legacy protocol
	if err := c.sendLine(fmt.Sprintf("SAMPLE_START %d", sampleID)); err != nil {
		return err
	}
	line, err := c.readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "OK") {
		return fmt.Errorf("control start failed: %s", line)
	}
	return nil
}

func (c *controlClient) StopSample(sampleID uint32) (SampleReport, error) {
	// Use RPC if available
	if c.rpcClient != nil {
		rpcResp, err := c.rpcClient.StopSample(sampleID)
		if err != nil {
			return SampleReport{}, err
		}
		// Convert RPC response to legacy SampleReport
		return convertRPCReport(rpcResp), nil
	}

	// Legacy protocol
	if err := c.sendLine(fmt.Sprintf("SAMPLE_STOP %d", sampleID)); err != nil {
		return SampleReport{}, err
	}
	line, err := c.readLine()
	if err != nil {
		return SampleReport{}, err
	}
	if strings.HasPrefix(line, "ERR") {
		return SampleReport{}, errors.New(line)
	}
	var report SampleReport
	if err := json.Unmarshal([]byte(line), &report); err != nil {
		return SampleReport{}, err
	}
	if report.SampleID != 0 && report.SampleID != sampleID {
		return SampleReport{}, fmt.Errorf("sample report id mismatch (got %d)", report.SampleID)
	}
	return report, nil
}

func (c *controlClient) sendLine(line string) error {
	if _, err := c.writer.WriteString(line + "\n"); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *controlClient) readLine() (string, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "", err
	}
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

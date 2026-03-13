//go:build !linux

package fbmeasure

import (
	"context"
	"fmt"
	"net"
)

type tcpRetransTest struct {
	expectedBytes uint64
	connCh        chan net.Conn
}

func (c *Client) TCPRetrans(_ context.Context, _ uint64) (RetransResult, error) {
	return RetransResult{}, fmt.Errorf("tcp retransmission measurement requires linux")
}

func (s *Server) handleTCPRetrans(_ context.Context, _ tcpRetransRequest) (tcpRetransResponse, error) {
	return tcpRetransResponse{}, fmt.Errorf("tcp retransmission measurement requires linux")
}

func (s *Server) handleTCPDataConn(_ context.Context, _ net.Conn) {}

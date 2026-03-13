package fbmeasure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
)

const defaultUDPReceiveWait = 100 * time.Millisecond

type Config struct {
	BindAddr       string
	Port           int
	UDPReceiveWait time.Duration
}

type Server struct {
	cfg    Config
	logger util.Logger

	mu      sync.Mutex
	tcpLn   net.Listener
	udpConn *net.UDPConn
	port    int

	udpPingTests    map[string]*udpPingTest
	udpLossTests    map[string]*udpLossTest
	tcpRetransTests map[string]*tcpRetransTest

	closeOnce sync.Once
	wg        sync.WaitGroup
}

func NewServer(cfg Config, logger util.Logger) *Server {
	if cfg.UDPReceiveWait <= 0 {
		cfg.UDPReceiveWait = defaultUDPReceiveWait
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "0.0.0.0"
	}
	return &Server{
		cfg:             cfg,
		logger:          logger,
		udpPingTests:    make(map[string]*udpPingTest),
		udpLossTests:    make(map[string]*udpLossTest),
		tcpRetransTests: make(map[string]*tcpRetransTest),
	}
}

func (s *Server) Start(ctx context.Context) error {
	addr := net.JoinHostPort(s.cfg.BindAddr, strconv.Itoa(s.cfg.Port))
	tcpLn, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	tcpAddr, ok := tcpLn.Addr().(*net.TCPAddr)
	if !ok {
		_ = tcpLn.Close()
		return fmt.Errorf("unexpected tcp listener addr type %T", tcpLn.Addr())
	}

	udpAddr := &net.UDPAddr{IP: tcpAddr.IP, Port: tcpAddr.Port, Zone: tcpAddr.Zone}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		_ = tcpLn.Close()
		return err
	}

	s.mu.Lock()
	s.tcpLn = tcpLn
	s.udpConn = udpConn
	s.port = tcpAddr.Port
	s.mu.Unlock()

	util.Event(s.logger, slog.LevelInfo, "fbmeasure.server_started",
		"listen.addr", net.JoinHostPort(s.cfg.BindAddr, strconv.Itoa(s.port)),
	)

	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.acceptLoop(ctx)
	}()
	go func() {
		defer s.wg.Done()
		s.serveUDP(ctx)
	}()
	return nil
}

func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		tcpLn := s.tcpLn
		udpConn := s.udpConn
		s.mu.Unlock()
		if tcpLn != nil {
			if closeErr := tcpLn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				err = closeErr
			}
		}
		if udpConn != nil {
			if closeErr := udpConn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				err = closeErr
			}
		}
	})
	return err
}

func (s *Server) Wait() {
	s.wg.Wait()
}

func (s *Server) acceptLoop(ctx context.Context) {
	for {
		s.mu.Lock()
		tcpLn := s.tcpLn
		s.mu.Unlock()
		if tcpLn == nil {
			return
		}
		conn, err := tcpLn.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			util.Event(s.logger, slog.LevelWarn, "fbmeasure.accept_failed", "error", err)
			continue
		}

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleTCPConn(ctx, c)
		}(conn)
	}
}

func (s *Server) handleTCPConn(ctx context.Context, conn net.Conn) {
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		_ = conn.Close()
		return
	}
	if bytes.Equal(prefix[:], []byte(tcpDataMarker)) {
		s.handleTCPDataConn(ctx, conn)
		return
	}
	defer conn.Close()

	reader := io.MultiReader(bytes.NewReader(prefix[:]), conn)
	for {
		var req controlRequest
		if err := readControlMessage(reader, &req); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return
			}
			util.Event(s.logger, slog.LevelWarn, "fbmeasure.control_read_failed", "error", err)
			return
		}

		resp := controlResponse{ID: req.ID, Op: req.Op, OK: true}
		payload, err := s.handleControlRequest(ctx, conn, req)
		if err != nil {
			resp.OK = false
			resp.Error = err.Error()
		} else if payload != nil {
			resp.Payload, err = marshalPayload(payload)
			if err != nil {
				resp.OK = false
				resp.Error = err.Error()
				resp.Payload = nil
			}
		}

		if err := writeControlMessage(conn, resp); err != nil {
			util.Event(s.logger, slog.LevelWarn, "fbmeasure.control_write_failed", "error", err)
			return
		}
	}
}

func (s *Server) handleControlRequest(ctx context.Context, _ net.Conn, req controlRequest) (any, error) {
	switch req.Op {
	case opPingTCP:
		var payload pingTCPRequest
		if err := unmarshalPayload(req.Payload, &payload); err != nil {
			return nil, err
		}
		return handlePingTCPRequest(payload), nil
	case opPingUDP:
		var payload pingUDPRequest
		if err := unmarshalPayload(req.Payload, &payload); err != nil {
			return nil, err
		}
		return s.handlePingUDP(ctx, payload)
	case opTCPRetrans:
		var payload tcpRetransRequest
		if err := unmarshalPayload(req.Payload, &payload); err != nil {
			return nil, err
		}
		return s.handleTCPRetrans(ctx, payload)
	case opUDPLoss:
		var payload udpLossRequest
		if err := unmarshalPayload(req.Payload, &payload); err != nil {
			return nil, err
		}
		return s.handleUDPLoss(ctx, payload)
	default:
		return nil, fmt.Errorf("unsupported operation %q", req.Op)
	}
}

func (s *Server) serveUDP(ctx context.Context) {
	buf := make([]byte, 64*1024)
	for {
		s.mu.Lock()
		udpConn := s.udpConn
		s.mu.Unlock()
		if udpConn == nil {
			return
		}
		if err := udpConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return
		}
		n, addr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			util.Event(s.logger, slog.LevelWarn, "fbmeasure.udp_read_failed", "error", err)
			continue
		}
		s.handleUDPPacket(udpConn, addr, buf[:n])
	}
}

package fbmeasure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	defaultListenAddress = "127.0.0.1:9876"
	maxTCPConnections    = 64
	maxTCPFrames         = 3
	tcpIdleTimeout       = 5 * time.Second
	udpPacketsPerSecond  = 100
)

type ServerConfig struct {
	ListenAddress string
}

type Server struct {
	tcpLn   *net.TCPListener
	udpConn *net.UDPConn
	addr    netip.AddrPort

	connSem chan struct{}
	conns   map[net.Conn]struct{}
	done    chan struct{}

	mu             sync.Mutex
	closed         bool
	serveStarted   bool
	udpWindowStart time.Time
	udpWindowCount int
	closeOnce      sync.Once
	serveWait      sync.WaitGroup
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.ListenAddress == "" {
		config.ListenAddress = defaultListenAddress
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", config.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve fbmeasure listen address: %w", err)
	}
	tcpLn, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return nil, err
	}
	boundTCP, ok := tcpLn.Addr().(*net.TCPAddr)
	if !ok {
		_ = tcpLn.Close()
		return nil, fmt.Errorf("unexpected TCP listener address type %T", tcpLn.Addr())
	}
	udpAddr := &net.UDPAddr{IP: boundTCP.IP, Port: boundTCP.Port, Zone: boundTCP.Zone}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		_ = tcpLn.Close()
		return nil, err
	}
	ip, ok := netip.AddrFromSlice(boundTCP.IP)
	if !ok {
		_ = tcpLn.Close()
		_ = udpConn.Close()
		return nil, fmt.Errorf("invalid bound address %q", boundTCP.IP)
	}
	return &Server{
		tcpLn:   tcpLn,
		udpConn: udpConn,
		addr:    netip.AddrPortFrom(ip, uint16(boundTCP.Port)),
		connSem: make(chan struct{}, maxTCPConnections),
		conns:   make(map[net.Conn]struct{}),
		done:    make(chan struct{}),
	}, nil
}

func (s *Server) Addr() netip.AddrPort {
	if s == nil {
		return netip.AddrPort{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

func (s *Server) Serve(ctx context.Context) error {
	if s == nil {
		return errors.New("fbmeasure server is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	if s.serveStarted {
		s.mu.Unlock()
		return errors.New("fbmeasure server already serving")
	}
	s.serveStarted = true
	s.mu.Unlock()

	s.serveWait.Add(2)
	go func() {
		defer s.serveWait.Done()
		s.acceptLoop(ctx)
	}()
	go func() {
		defer s.serveWait.Done()
		s.serveUDP(ctx)
	}()
	select {
	case <-ctx.Done():
	case <-s.done:
	}
	_ = s.Close()
	s.serveWait.Wait()
	return nil
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	var result error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		tcpLn := s.tcpLn
		udpConn := s.udpConn
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
		close(s.done)
		if tcpLn != nil {
			if err := tcpLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				result = err
			}
		}
		if udpConn != nil {
			if err := udpConn.Close(); err != nil && !errors.Is(err, net.ErrClosed) && result == nil {
				result = err
			}
		}
	})
	return result
}

func (s *Server) acceptLoop(ctx context.Context) {
	for {
		if err := s.tcpLn.SetDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			return
		}
		conn, err := s.tcpLn.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			continue
		}
		select {
		case s.connSem <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.serveWait.Add(1)
		go func() {
			defer s.serveWait.Done()
			defer func() {
				_ = conn.Close()
				<-s.connSem
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
			}()
			s.handleTCP(ctx, conn)
		}()
	}
}

func (s *Server) handleTCP(ctx context.Context, conn net.Conn) {
	for i := 0; i < maxTCPFrames; i++ {
		if ctx.Err() != nil {
			return
		}
		_ = conn.SetDeadline(time.Now().Add(tcpIdleTimeout))
		var raw [frameSize]byte
		if _, err := io.ReadFull(conn, raw[:]); err != nil {
			return
		}
		decoded, err := parseFrame(raw[:])
		if err != nil {
			return
		}
		if err := writeFull(conn, decoded[:]); err != nil {
			return
		}
	}
}

func (s *Server) serveUDP(ctx context.Context) {
	var buf [frameSize + 1]byte
	for {
		if err := s.udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			return
		}
		n, addr, err := s.udpConn.ReadFromUDP(buf[:])
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			continue
		}
		decoded, err := parseFrame(buf[:n])
		if err != nil || !s.allowUDPPacket() {
			continue
		}
		_, _ = s.udpConn.WriteToUDP(decoded[:], addr)
	}
}

func (s *Server) allowUDPPacket() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.udpWindowStart.IsZero() || now.Sub(s.udpWindowStart) >= time.Second {
		s.udpWindowStart = now
		s.udpWindowCount = 0
	}
	if s.udpWindowCount >= udpPacketsPerSecond {
		return false
	}
	s.udpWindowCount++
	return true
}

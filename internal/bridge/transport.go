package bridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const SessionTimeout = 2 * time.Second
const HeartbeatInterval = 400 * time.Millisecond
const MaxFrame = 2048

type Session struct {
	conn    net.Conn
	scanner *bufio.Scanner
	mu      sync.Mutex
}

func session(c net.Conn) *Session {
	s := bufio.NewScanner(c)
	s.Buffer(make([]byte, MaxFrame), MaxFrame)
	return &Session{conn: c, scanner: s}
}
func (s *Session) Close() error { return s.conn.Close() }
func (s *Session) Send(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxFrame {
		return errors.New("frame too large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(SessionTimeout))
	_, err = io.Copy(s.conn, bytes.NewReader(append(b, '\n')))
	return err
}
func (s *Session) Receive() (Event, error) {
	s.conn.SetReadDeadline(time.Now().Add(SessionTimeout))
	var e Event
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return e, err
		}
		return e, io.EOF
	}
	d := json.NewDecoder(bytes.NewReader(s.scanner.Bytes()))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return e, err
	}
	if d.Decode(new(any)) != io.EOF {
		return e, errors.New("trailing data")
	}
	return e, nil
}
func Dial(ctx context.Context, addr string, key []byte) (*Session, error) {
	cfg, err := TLSConfig(key, false)
	if err != nil {
		return nil, err
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: SessionTimeout}, Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := session(conn)
	e, err := s.Receive()
	if err != nil || e.Kind != "ready" {
		s.Close()
		return nil, fmt.Errorf("receiver unavailable: %s (%v)", e.Kind, err)
	}
	return s, nil
}

// Serve grants one authenticated controller exclusive ownership of the sink.
// Eight concurrent handshakes are allowed; each expires in two seconds.
func Serve(ctx context.Context, ln net.Listener, key []byte, newMachine func() *Machine) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cfg, err := TLSConfig(key, true)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	defer ln.Close()
	var busy atomic.Bool
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer conn.Close()
			stopConn := context.AfterFunc(ctx, func() { conn.Close() })
			defer stopConn()
			conn.SetDeadline(time.Now().Add(SessionTimeout))
			tc := tls.Server(conn, cfg)
			if tc.HandshakeContext(ctx) != nil {
				return
			}
			s := session(tc)
			if !busy.CompareAndSwap(false, true) {
				s.Send(Event{Kind: "busy"})
				return
			}
			defer busy.Store(false)
			m := newMachine()
			defer m.Release()
			if s.Send(Event{Kind: "ready"}) != nil {
				return
			}
			for {
				e, err := s.Receive()
				if err != nil {
					return
				}
				r, err := m.Handle(e)
				if err != nil {
					return
				}
				if r.Kind == "" {
					r.Kind = "ok"
				}
				if s.Send(r) != nil {
					return
				}
			}
		}()
	}
}

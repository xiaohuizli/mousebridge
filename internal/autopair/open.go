package autopair

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mousebridge/internal/bridge"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxOpenPeers = 16

type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OpenService is an explicitly open LAN mode. Discover=true authorizes every
// other enabled LAN instance; it does not claim authenticated device trust.
// Each connection still has independent TLS and ephemeral input-channel keys.
type OpenService struct {
	cfg         Config
	ID          string
	mu          sync.Mutex
	emitMu      sync.Mutex
	discover    bool
	enabled     bool
	edge        string
	selected    string
	generation  uint64
	sessions    map[string]bool
	devices     map[string]Device
	blocked     map[string]time.Time
	controlSlot chan struct{}
	listener    net.Listener
	udp         *net.UDPConn
}

func NewOpen(cfg Config, discover bool) (*OpenService, error) {
	base, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &OpenService{cfg: base.cfg, ID: base.ID, discover: discover, enabled: cfg.Enabled, edge: base.cfg.Edge,
		sessions: make(map[string]bool), devices: make(map[string]Device), blocked: make(map[string]time.Time), controlSlot: make(chan struct{}, 1)}, nil
}
func (s *OpenService) emit(event Event) { s.emitMu.Lock(); defer s.emitMu.Unlock(); s.cfg.Emit(event) }

func (s *OpenService) sharingError(err error, id, role string) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return
	}
	var networkError net.Error
	if errors.As(err, &networkError) || strings.HasPrefix(err.Error(), "receiver unavailable") {
		s.emit(Event{Kind: "retrying", ID: id, Role: role, Message: "输入连接正在自动恢复"})
		return
	}
	s.emit(Event{Kind: "error", ID: id, Role: role, Message: err.Error()})
}
func (s *OpenService) settings() (bool, bool, string, string, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discover, s.enabled, s.edge, s.selected, s.generation
}
func (s *OpenService) Command(command Command) {
	s.mu.Lock()
	switch command.Kind {
	case "settings":
		if command.Edge != "left" && command.Edge != "right" {
			s.mu.Unlock()
			return
		}
		if s.discover != command.Discover {
			s.generation++
		}
		s.discover = command.Discover
		s.enabled = command.Enabled
		s.edge = command.Edge
	case "select":
		if _, ok := s.devices[command.ID]; ok {
			s.selected = command.ID
		}
	}
	s.mu.Unlock()
	s.roster()
}
func (s *OpenService) reserve(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.discover || s.sessions[id] || len(s.sessions) >= maxOpenPeers || time.Now().Before(s.blocked[id]) {
		return false
	}
	s.sessions[id] = true
	return true
}
func (s *OpenService) release(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	delete(s.devices, id)
	// Bound discovery retry bookkeeping even on a busy local network.
	if len(s.blocked) > 64 {
		for key := range s.blocked {
			delete(s.blocked, key)
		}
	}
	s.blocked[id] = time.Now().Add(time.Second)
	if s.selected == id {
		s.selected = ""
		for candidate := range s.devices {
			if s.selected == "" || candidate < s.selected {
				s.selected = candidate
			}
		}
	}
	s.mu.Unlock()
	s.roster()
}
func (s *OpenService) roster() {
	// Serialize the snapshot itself, so an older roster cannot be emitted after
	// a newer one when several peers join or leave at the same time.
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.mu.Lock()
	peers := make([]Device, 0, len(s.devices))
	for _, device := range s.devices {
		peers = append(peers, device)
	}
	selected := s.selected
	s.mu.Unlock()
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	s.cfg.Emit(Event{Kind: "devices", Peers: peers, Selected: selected})
}
func (s *OpenService) add(id, name string) {
	s.mu.Lock()
	s.devices[id] = Device{ID: id, Name: cleanName(name)}
	if s.selected == "" {
		s.selected = id
	}
	s.mu.Unlock()
	s.roster()
}

// LAN addresses must be on a directly attached broadcast network or loopback.
// An address being RFC1918 alone is insufficient (it may be remotely routed).
func onLink(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			_, network, err := net.ParseCIDR(addr.String())
			if err == nil && network.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func (s *OpenService) tlsConfig(server bool, expected string) (*tls.Config, error) {
	identity, err := bridge.TLSConfig(s.cfg.Identity, false)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: identity.Certificates, NextProtos: []string{"mousebridge/lan/1"}, InsecureSkipVerify: true}
	if server {
		cfg.ClientAuth = tls.RequireAnyClientCert
	}
	// This verifies protocol and channel consistency, not an approved identity.
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		id, err := peerID(state)
		if err != nil {
			return err
		}
		if state.NegotiatedProtocol != "mousebridge/lan/1" || id == s.ID || (expected != "" && id != expected) {
			return errors.New("LAN channel identity mismatch")
		}
		return nil
	}
	return cfg, nil
}

func (s *OpenService) Run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ln, err := net.Listen("tcp4", s.cfg.Listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	udp := s.cfg.DiscoveryConn
	if udp == nil {
		address, resolveErr := net.ResolveUDPAddr("udp4", s.cfg.DiscoveryListen)
		if resolveErr != nil {
			return resolveErr
		}
		udp, err = net.ListenUDP("udp4", address)
		if err != nil {
			return err
		}
	}
	defer udp.Close()
	s.listener, s.udp = ln, udp
	stop := context.AfterFunc(ctx, func() { ln.Close(); udp.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer func() { cancel(); ln.Close(); udp.Close(); wg.Wait() }()
	s.emit(Event{Kind: "searching", ID: s.ID, Message: "局域网开放发现已启动，无需配对确认"})
	wg.Add(1)
	go func() { defer wg.Done(); s.advertise(ctx) }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		buffer := make([]byte, 2049)
		for ctx.Err() == nil {
			n, from, err := udp.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			var peer beacon
			if n > 2048 || json.Unmarshal(buffer[:n], &peer) != nil || peer.Version != 2 || !validID(peer.ID) || peer.ID <= s.ID || peer.Port < 1 || peer.Port > 65535 || len(peer.Name) > 128 || !onLink(from.IP) || !s.reserve(peer.ID) {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer s.release(peer.ID)
				cfg, _ := s.tlsConfig(false, peer.ID)
				dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 3 * time.Second}, Config: cfg}
				raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(from.IP.String(), strconv.Itoa(peer.Port)))
				if err != nil {
					return
				}
				defer raw.Close()
				s.session(ctx, raw.(*tls.Conn), peer.ID)
			}()
		}
	}()
	slots := make(chan struct{}, 8)
	for ctx.Err() == nil {
		raw, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		from, _, _ := net.SplitHostPort(raw.RemoteAddr().String())
		discover, _, _, _, _ := s.settings()
		if !discover || !onLink(net.ParseIP(from)) {
			raw.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			raw.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			defer raw.Close()
			cfg, _ := s.tlsConfig(true, "")
			conn := tls.Server(raw, cfg)
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			if conn.HandshakeContext(ctx) != nil {
				return
			}
			id, err := peerID(conn.ConnectionState())
			if err != nil || id >= s.ID || !s.reserve(id) {
				return
			}
			defer s.release(id)
			s.session(ctx, conn, id)
		}()
	}
	return nil
}

func (s *OpenService) advertise(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		discover, _, _, _, _ := s.settings()
		if discover {
			targets := s.cfg.Targets
			if len(targets) == 0 {
				targets = broadcasts()
			}
			data, _ := json.Marshal(beacon{Version: 2, ID: s.ID, Name: cleanName(s.cfg.Name), Port: s.listener.Addr().(*net.TCPAddr).Port})
			for _, target := range targets {
				if addr, err := net.ResolveUDPAddr("udp4", target); err == nil && (addr.IP.Equal(net.IPv4bcast) || addr.IP.IsLoopback() || onLink(addr.IP)) {
					s.udp.WriteToUDP(data, addr)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type openState struct {
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled,omitempty"`
	Port    int    `json:"port,omitempty"`
}

func (s *OpenService) session(parent context.Context, conn *tls.Conn, id string) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, maxFrame), maxFrame)
	write := func(message openState) error {
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		return json.NewEncoder(conn).Encode(message)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if write(openState{Kind: "open", Name: cleanName(s.cfg.Name)}) != nil || !scanner.Scan() {
		return
	}
	var hello openState
	if json.Unmarshal(scanner.Bytes(), &hello) != nil || hello.Kind != "open" || len(hello.Name) > 128 {
		return
	}
	discover, _, _, _, generation := s.settings()
	if !discover {
		return
	}
	state := conn.ConnectionState()
	key, err := state.ExportKeyingMaterial("mousebridge/lan/v1/input-key", nil, 32)
	if err != nil {
		return
	}
	s.add(id, hello.Name)
	s.emit(Event{Kind: "connected", ID: id, Name: cleanName(hello.Name), Message: "已自动连接，无需配对"})
	messages := make(chan openState, 1)
	readDone := make(chan struct{})
	defer close(readDone)
	go func() {
		defer close(messages)
		for {
			conn.SetReadDeadline(time.Now().Add(4 * time.Second))
			if !scanner.Scan() {
				return
			}
			var message openState
			if json.Unmarshal(scanner.Bytes(), &message) != nil || message.Kind != "state" || message.Port < 0 || message.Port > 65535 {
				return
			}
			select {
			case messages <- message:
			case <-ctx.Done():
				return
			case <-readDone:
				return
			}
		}
	}()
	var receiveCancel, controlCancel context.CancelFunc
	var receiveDone, controlDone chan error
	var localPort int
	var remote openState
	var controlEdge string
	var receiveRetry, controlRetry time.Time
	stopReceive := func() {
		if receiveCancel != nil {
			receiveCancel()
			<-receiveDone
			receiveCancel = nil
			receiveDone = nil
		}
		localPort = 0
	}
	stopControl := func() {
		if controlCancel != nil {
			controlCancel()
			<-controlDone
			controlCancel = nil
			controlDone = nil
		}
	}
	defer func() { stopControl(); stopReceive() }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-messages:
			if !ok {
				return
			}
			remote = message
		case err := <-receiveDone:
			receiveCancel()
			receiveCancel = nil
			receiveDone = nil
			localPort = 0
			receiveRetry = time.Now().Add(4 * time.Second)
			s.sharingError(err, id, "receive")
		case err := <-controlDone:
			controlCancel()
			controlCancel = nil
			controlDone = nil
			controlRetry = time.Now().Add(4 * time.Second)
			s.sharingError(err, id, "control")
		case <-ticker.C:
			discover, enabled, edge, selected, currentGeneration := s.settings()
			if !discover || currentGeneration != generation {
				return
			}
			sharing := enabled && remote.Enabled
			if !sharing {
				stopControl()
				stopReceive()
			}
			if sharing && receiveCancel == nil && time.Now().After(receiveRetry) {
				ln, err := net.Listen("tcp4", ":0")
				if err != nil {
					s.emit(Event{Kind: "error", ID: id, Role: "receive", Message: err.Error()})
					receiveRetry = time.Now().Add(4 * time.Second)
				} else {
					localPort = ln.Addr().(*net.TCPAddr).Port
					receiveCtx, cancel := context.WithCancel(ctx)
					receiveCancel = cancel
					receiveDone = make(chan error, 1)
					result := receiveDone
					go func() { defer ln.Close(); result <- s.cfg.Receive(receiveCtx, ln, key) }()
					s.emit(Event{Kind: "receiving", ID: id, Name: cleanName(hello.Name)})
				}
			}
			if controlCancel != nil && (!sharing || selected != id || edge != controlEdge || remote.Port == 0) {
				stopControl()
			}
			if sharing && selected == id && remote.Port > 0 && controlCancel == nil && time.Now().After(controlRetry) {
				controlCtx, cancel := context.WithCancel(ctx)
				controlCancel = cancel
				controlDone = make(chan error, 1)
				result := controlDone
				controlEdge = edge
				host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
				address := net.JoinHostPort(host, strconv.Itoa(remote.Port))
				go func() {
					select {
					case s.controlSlot <- struct{}{}:
					case <-controlCtx.Done():
						result <- nil
						return
					}
					defer func() { <-s.controlSlot }()
					s.emit(Event{Kind: "controlling", ID: id, Name: cleanName(hello.Name)})
					result <- s.cfg.Control(controlCtx, address, key, edge)
				}()
			}
			if write(openState{Kind: "state", Enabled: enabled, Port: localPort}) != nil {
				return
			}
		}
	}
}

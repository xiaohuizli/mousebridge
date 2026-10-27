// Package autopair discovers two desktops and establishes explicitly approved
// per-device trust. Discovery is public; no input access is granted by discovery.
package autopair

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mousebridge/internal/bridge"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const DiscoveryPort = 42049
const maxFrame = 4096

type Event struct {
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Token string `json:"token,omitempty"`
	Code  string `json:"code,omitempty"`
	// Key is emitted only over the private GUI pipe, never logged or broadcast.
	Key      string   `json:"key,omitempty"`
	Role     string   `json:"role,omitempty"`
	Message  string   `json:"message,omitempty"`
	Address  string   `json:"address,omitempty"`
	Peers    []Device `json:"peers,omitempty"`
	Selected string   `json:"selected,omitempty"`
}

type Command struct {
	Kind     string `json:"kind"`
	Token    string `json:"token,omitempty"`
	Enabled  bool   `json:"enabled"`
	Role     string `json:"role,omitempty"`
	Edge     string `json:"edge,omitempty"`
	ID       string `json:"id,omitempty"`
	Discover bool   `json:"discover"`
}

type Config struct {
	Identity        []byte
	Trust           map[string]string
	Name            string
	Listen          string // pairing TCP; defaults to an ephemeral port
	DiscoveryListen string
	// DiscoveryConn optionally transfers ownership of an already-bound socket.
	// Integration tests use this to avoid closing/rebinding ephemeral UDP ports.
	DiscoveryConn *net.UDPConn
	Targets       []string // empty: all local IPv4 subnet broadcast addresses
	Enabled       bool
	Role          string // auto, control, receive
	Edge          string
	Emit          func(Event)
	Receive       func(context.Context, net.Listener, []byte) error
	Control       func(context.Context, string, []byte, string) error
}

type beacon struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Port    int    `json:"port"`
}

type wire struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	Known    bool   `json:"known,omitempty"`
	Proof    []byte `json:"proof,omitempty"`
	Enabled  bool   `json:"enabled,omitempty"`
	Role     string `json:"role,omitempty"`
	Port     int    `json:"port,omitempty"`
	Approved bool   `json:"approved,omitempty"`
}

type Service struct {
	cfg      Config
	ID       string
	mu       sync.Mutex
	emitMu   sync.Mutex
	pending  map[string]chan bool
	blocked  map[string]time.Time
	active   atomic.Bool
	listener net.Listener
	udp      *net.UDPConn
}

func New(cfg Config) (*Service, error) {
	if len(cfg.Identity) != 32 || cfg.Receive == nil || cfg.Control == nil || cfg.Emit == nil {
		return nil, errors.New("auto pairing requires identity, emitter and sharing callbacks")
	}
	cert, err := bridge.TLSConfig(cfg.Identity, false)
	if err != nil {
		return nil, err
	}
	if cfg.Listen == "" {
		cfg.Listen = ":0"
	}
	if cfg.DiscoveryListen == "" {
		cfg.DiscoveryListen = ":42049"
	}
	if cfg.Role == "" {
		cfg.Role = "auto"
	}
	if cfg.Edge == "" {
		cfg.Edge = "right"
	}
	if !validRole(cfg.Role) || (cfg.Edge != "left" && cfg.Edge != "right") {
		return nil, errors.New("invalid auto role or edge")
	}
	trust := make(map[string]string)
	for id, value := range cfg.Trust {
		if !validID(id) {
			return nil, errors.New("invalid trusted peer identity")
		}
		if _, err := bridge.ParseKey(value); err != nil {
			return nil, err
		}
		trust[id] = value
	}
	cfg.Trust = trust
	// The stable identity is the certificate's Ed25519 public key hash.
	parsed, err := tlsLeaf(cert.Certificates[0])
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, ID: parsed, pending: make(map[string]chan bool), blocked: make(map[string]time.Time)}, nil
}

func (s *Service) emit(e Event)  { s.emitMu.Lock(); defer s.emitMu.Unlock(); s.cfg.Emit(e) }
func validID(id string) bool     { b, err := hex.DecodeString(id); return err == nil && len(b) == 32 }
func validRole(role string) bool { return role == "auto" || role == "control" || role == "receive" }

func (s *Service) Command(c Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch c.Kind {
	case "approve", "reject":
		if ch := s.pending[c.Token]; ch != nil {
			select {
			case ch <- c.Kind == "approve":
			default:
			}
		}
	case "settings":
		if !validRole(c.Role) || (c.Edge != "left" && c.Edge != "right") {
			return
		}
		s.cfg.Enabled, s.cfg.Role, s.cfg.Edge = c.Enabled, c.Role, c.Edge
	}
}

func (s *Service) settings() (bool, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Enabled, s.cfg.Role, s.cfg.Edge
}
func (s *Service) trusted(id string) string { s.mu.Lock(); defer s.mu.Unlock(); return s.cfg.Trust[id] }
func (s *Service) allowed(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().After(s.blocked[id]) && (len(s.cfg.Trust) == 0 || s.cfg.Trust[id] != "")
}

// Run owns its listeners and all active sessions until context cancellation.
func (s *Service) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	ln, err := net.Listen("tcp4", s.cfg.Listen)
	if err != nil {
		cancel()
		return err
	}
	defer ln.Close()
	udp := s.cfg.DiscoveryConn
	if udp == nil {
		addr, resolveErr := net.ResolveUDPAddr("udp4", s.cfg.DiscoveryListen)
		if resolveErr != nil {
			cancel()
			return resolveErr
		}
		udp, err = net.ListenUDP("udp4", addr)
		if err != nil {
			cancel()
			return err
		}
	}
	defer udp.Close()
	s.listener, s.udp = ln, udp
	stop := context.AfterFunc(ctx, func() { ln.Close(); udp.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer func() { cancel(); ln.Close(); udp.Close(); wg.Wait() }()
	s.emit(Event{Kind: "searching", ID: s.ID, Message: "正在自动寻找同一局域网的电脑"})
	wg.Add(1)
	go func() { defer wg.Done(); s.advertise(ctx) }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 2049)
		for ctx.Err() == nil {
			n, from, err := udp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var b beacon
			if n > 2048 || json.Unmarshal(buf[:n], &b) != nil || b.Version != 1 || !validID(b.ID) || b.ID == s.ID ||
				b.Port < 1 || b.Port > 65535 || len(b.Name) > 128 || !s.allowed(b.ID) {
				continue
			}
			// One deterministic initiator avoids simultaneous opposing control sessions.
			if s.ID < b.ID && s.active.CompareAndSwap(false, true) {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer s.active.Store(false)
					s.connect(ctx, net.JoinHostPort(from.IP.String(), strconv.Itoa(b.Port)), b.ID)
				}()
			}
		}
	}()
	// Limit unauthenticated TLS handshakes independently of a paired session.
	slots := make(chan struct{}, 4)
	for ctx.Err() == nil {
		raw, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
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
			state := conn.ConnectionState()
			id, err := peerID(state)
			if err != nil || id >= s.ID || !s.allowed(id) || !s.active.CompareAndSwap(false, true) {
				return
			}
			defer s.active.Store(false)
			s.session(ctx, conn, id)
		}()
	}
	return nil
}

func (s *Service) advertise(ctx context.Context) {
	targets := s.cfg.Targets
	if len(targets) == 0 {
		targets = broadcasts()
	}
	data, _ := json.Marshal(beacon{Version: 1, ID: s.ID, Name: cleanName(s.cfg.Name), Port: s.listener.Addr().(*net.TCPAddr).Port})
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		for _, target := range targets {
			if addr, err := net.ResolveUDPAddr("udp4", target); err == nil {
				s.udp.WriteToUDP(data, addr)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func broadcasts() []string {
	found := map[string]bool{"255.255.255.255:42049": true}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, network, err := net.ParseCIDR(addr.String())
			if err != nil || ip.To4() == nil || len(network.Mask) != 4 {
				continue
			}
			ip = ip.To4()
			for i := 0; i < 4; i++ {
				ip[i] |= ^network.Mask[i]
			}
			found[net.JoinHostPort(ip.String(), "42049")] = true
		}
	}
	result := make([]string, 0, len(found))
	for addr := range found {
		result = append(result, addr)
	}
	return result
}

func (s *Service) tlsConfig(server bool, expected string) (*tls.Config, error) {
	base, err := bridge.TLSConfig(s.cfg.Identity, false)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: base.Certificates, NextProtos: []string{"mousebridge/auto/1"}}
	// Unknown peers have no CA identity: TLS confidentiality is authenticated by
	// both users comparing the TLS-exporter code before any trust/input is granted.
	cfg.InsecureSkipVerify = true
	if server {
		cfg.ClientAuth = tls.RequireAnyClientCert
	}
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		id, err := peerID(state)
		if err != nil {
			return err
		}
		if state.NegotiatedProtocol != "mousebridge/auto/1" || (expected != "" && expected != id) {
			return errors.New("auto peer identity mismatch")
		}
		if !s.allowed(id) {
			return errors.New("peer not allowed")
		}
		return nil
	}
	return cfg, nil
}

func (s *Service) connect(ctx context.Context, address, id string) {
	cfg, _ := s.tlsConfig(false, id)
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 3 * time.Second}, Config: cfg}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		s.block(id, 3*time.Second)
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	s.session(ctx, conn.(*tls.Conn), id)
}
func (s *Service) block(id string, wait time.Duration) {
	s.mu.Lock()
	s.blocked[id] = time.Now().Add(wait)
	s.mu.Unlock()
}

func (s *Service) session(parent context.Context, conn *tls.Conn, id string) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(120 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, maxFrame), maxFrame)
	read := func() (wire, error) {
		var msg wire
		if !scanner.Scan() {
			return msg, io.EOF
		}
		err := json.Unmarshal(scanner.Bytes(), &msg)
		return msg, err
	}
	write := func(msg wire) error {
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		return json.NewEncoder(conn).Encode(msg)
	}
	known := s.trusted(id)
	state := conn.ConnectionState()
	binding, err := state.ExportKeyingMaterial("mousebridge/auto/v1/binding", nil, 32)
	if err != nil {
		return
	}
	proof := func(key string, identity string) []byte {
		b, _ := bridge.ParseKey(key)
		h := hmac.New(sha256.New, b)
		h.Write(binding)
		h.Write([]byte(identity))
		return h.Sum(nil)
	}
	intro := wire{Kind: "hello", Name: cleanName(s.cfg.Name), Known: known != ""}
	if known != "" {
		intro.Proof = proof(known, s.ID)
	}
	if write(intro) != nil {
		return
	}
	remote, err := read()
	if err != nil || remote.Kind != "hello" || len(remote.Name) > 128 {
		return
	}
	name := cleanName(remote.Name)
	if known != "" && remote.Known {
		if !hmac.Equal(remote.Proof, proof(known, id)) {
			s.emit(Event{Kind: "error", Message: "设备身份验证失败，请在两端忘记配对后重试"})
			s.block(id, 30*time.Second)
			return
		}
	} else {
		token := hex.EncodeToString(binding)
		sas, _ := state.ExportKeyingMaterial("mousebridge/auto/v1/display-code", nil, 32)
		code := fmt.Sprintf("%08d", binary.BigEndian.Uint32(sas[:4])%100000000)
		approvals := make(chan bool, 1)
		s.mu.Lock()
		s.pending[token] = approvals
		s.mu.Unlock()
		defer func() { s.mu.Lock(); delete(s.pending, token); s.mu.Unlock() }()
		s.emit(Event{Kind: "confirm", ID: id, Name: name, Token: token, Code: code})
		reject := func() {
			s.emit(Event{Kind: "rejected", Token: token, Message: "配对未获双方确认，未开启鼠标控制"})
			s.block(id, 30*time.Second)
		}
		// Read the remote response while waiting for the local user, so a denial
		// or disconnected computer dismisses the other sheet immediately.
		type approvalReply struct {
			message wire
			err     error
		}
		replies := make(chan approvalReply, 1)
		go func() { message, err := read(); replies <- approvalReply{message, err} }()
		timer := time.NewTimer(90 * time.Second)
		defer timer.Stop()
		localApproved, remoteApproved := false, false
		for !localApproved || !remoteApproved {
			select {
			case approved := <-approvals:
				approvals = nil
				if write(wire{Kind: "approval", Approved: approved}) != nil || !approved {
					reject()
					return
				}
				localApproved = true
			case reply := <-replies:
				replies = nil
				if reply.err != nil || reply.message.Kind != "approval" || !reply.message.Approved {
					reject()
					return
				}
				remoteApproved = true
			case <-ctx.Done():
				return
			case <-timer.C:
				write(wire{Kind: "approval", Approved: false})
				reject()
				return
			}
		}
		key, _ := state.ExportKeyingMaterial("mousebridge/auto/v1/shared-key", nil, 32)
		known = base64.RawURLEncoding.EncodeToString(key)
		s.mu.Lock()
		s.cfg.Trust[id] = known
		s.mu.Unlock()
		s.emit(Event{Kind: "trusted", ID: id, Name: name, Key: known})
	}
	s.emit(Event{Kind: "paired", ID: id, Name: name, Message: "配对成功，将自动连接"})
	key, _ := bridge.ParseKey(known)
	s.share(ctx, conn, scanner, write, id, name, key)
	if ctx.Err() == nil {
		s.emit(Event{Kind: "disconnected", Name: name, Message: "连接已断开，正在自动重连"})
	}
	s.block(id, 2*time.Second)
}

// resolveRole gives the same answer at both ends, with optional user overrides.
func resolveRole(localID, remoteID, local, remote string) string {
	if !validRole(local) || !validRole(remote) {
		return ""
	}
	if local != "auto" && remote != "auto" {
		if local == remote {
			return ""
		}
		return local
	}
	if local != "auto" {
		return local
	}
	if remote == "control" {
		return "receive"
	}
	if remote == "receive" {
		return "control"
	}
	if localID < remoteID {
		return "control"
	}
	return "receive"
}

func (s *Service) share(ctx context.Context, conn *tls.Conn, scanner *bufio.Scanner, write func(wire) error, id, name string, key []byte) {
	messages := make(chan wire, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(messages)
		for {
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if !scanner.Scan() {
				return
			}
			var msg wire
			if json.Unmarshal(scanner.Bytes(), &msg) != nil || msg.Kind != "state" || msg.Port < 0 || msg.Port > 65535 || !validRole(msg.Role) {
				return
			}
			select {
			case messages <- msg:
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()
	var remote wire
	var shareCancel context.CancelFunc
	var shareDone chan error
	var current, currentEdge string
	var localPort int
	var retry time.Time
	stopShare := func() {
		if shareCancel != nil {
			shareCancel()
			<-shareDone
			shareCancel = nil
			shareDone = nil
		}
		current = ""
		localPort = 0
	}
	defer stopShare()
	defer conn.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-messages:
			if !ok {
				return
			}
			remote = msg
		case err := <-shareDone:
			if shareCancel != nil {
				shareCancel()
			}
			shareDone = nil
			shareCancel = nil
			current = ""
			localPort = 0
			retry = time.Now().Add(3 * time.Second)
			if err != nil {
				s.emit(Event{Kind: "error", Message: err.Error()})
			}
		case <-ticker.C:
			enabled, preferred, edge := s.settings()
			desired := ""
			if enabled && remote.Enabled {
				desired = resolveRole(s.ID, id, preferred, remote.Role)
			}
			if current != "" && (desired != current || edge != currentEdge) {
				stopShare()
			}
			if desired != "" && current == "" && time.Now().After(retry) {
				host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
				var ln net.Listener
				var err error
				if desired == "receive" {
					ln, err = net.Listen("tcp4", ":0")
					if err == nil {
						localPort = ln.Addr().(*net.TCPAddr).Port
					}
				}
				if err != nil {
					s.emit(Event{Kind: "error", Message: err.Error()})
					retry = time.Now().Add(3 * time.Second)
				} else if desired == "receive" || remote.Port > 0 {
					shareCtx, cancel := context.WithCancel(ctx)
					shareCancel = cancel
					shareDone = make(chan error, 1)
					current, currentEdge = desired, edge
					address := net.JoinHostPort(host, strconv.Itoa(remote.Port))
					result := shareDone
					go func(role string) {
						if role == "receive" {
							result <- s.cfg.Receive(shareCtx, ln, key)
						} else {
							result <- s.cfg.Control(shareCtx, address, key, edge)
						}
					}(desired)
					s.emit(Event{Kind: "sharing", ID: id, Name: name, Role: desired, Address: address})
				}
			}
			if desired == "" && current == "" {
				s.emit(Event{Kind: "waiting", Name: name, Message: "已匹配，等待两端开启共享；角色冲突时请选择自动分配"})
			}
			if write(wire{Kind: "state", Enabled: enabled, Role: preferred, Port: localPort}) != nil {
				return
			}
		}
	}
}

func cleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	for len(name) > 128 {
		_, size := lastRune(name)
		name = name[:len(name)-size]
	}
	return name
}

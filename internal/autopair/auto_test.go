package autopair

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"io"
	"mousebridge/internal/bridge"
	"mousebridge/internal/desktop"
	"net"
	"testing"
	"time"
)

type testPeer struct {
	service  *Service
	events   chan Event
	cancel   context.CancelFunc
	done     chan error
	verified chan struct{}
	stopped  chan struct{}
}

func udpListeners(t *testing.T, count int) ([]*net.UDPConn, []string) {
	t.Helper()
	connections := make([]*net.UDPConn, count)
	addresses := make([]string, count)
	for i := range connections {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		connections[i] = conn
		addresses[i] = conn.LocalAddr().String()
		t.Cleanup(func() { conn.Close() })
	}
	return connections, addresses
}
func identity(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}
func startPeers(t *testing.T, keys [2][]byte, trust [2]map[string]string) [2]*testPeer {
	t.Helper()
	connections, addresses := udpListeners(t, 2)
	var peers [2]*testPeer
	for i := range peers {
		p := &testPeer{events: make(chan Event, 256), done: make(chan error, 1), verified: make(chan struct{}, 8), stopped: make(chan struct{}, 8)}
		cfg := Config{Identity: keys[i], Trust: trust[i], Name: []string{"Left", "Right"}[i], Listen: "127.0.0.1:0", DiscoveryConn: connections[i],
			Targets: []string{addresses[1-i]}, Enabled: true, Emit: func(e Event) { p.events <- e },
			Receive: func(ctx context.Context, ln net.Listener, key []byte) error {
				defer func() { p.stopped <- struct{}{} }()
				return bridge.Serve(ctx, ln, key, func() *bridge.Machine {
					return bridge.NewMachine(&desktop.Recorder{W: 100, H: 100, Writer: io.Discard}, 100, 100)
				})
			},
			Control: func(ctx context.Context, addr string, key []byte, edge string) error {
				defer func() { p.stopped <- struct{}{} }()
				session, err := bridge.Dial(ctx, addr, key)
				if err != nil {
					return err
				}
				defer session.Close()
				stop := context.AfterFunc(ctx, func() { session.Close() })
				defer stop()
				verified := false
				for {
					if err := session.Send(bridge.Event{Kind: "ping"}); err != nil {
						return err
					}
					if _, err := session.Receive(); err != nil {
						return err
					}
					if !verified {
						p.verified <- struct{}{}
						verified = true
					}
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(50 * time.Millisecond):
					}
				}
			}}
		service, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		p.service = service
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		go func() { p.done <- service.Run(ctx) }()
		peers[i] = p
	}
	t.Cleanup(func() { stopPeers(t, peers) })
	for _, p := range peers {
		await(t, p, "searching")
	}
	return peers
}
func stopPeers(t *testing.T, peers [2]*testPeer) {
	t.Helper()
	for _, p := range peers {
		if p.cancel != nil {
			p.cancel()
		}
	}
	for _, p := range peers {
		if p.cancel == nil {
			continue
		}
		select {
		case err := <-p.done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("auto peer did not stop")
		}
		p.cancel = nil
	}
}
func await(t *testing.T, p *testPeer, kind string) Event {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-p.events:
			if event.Kind == kind {
				return event
			}
			if event.Kind == "error" {
				t.Fatalf("unexpected service error: %s", event.Message)
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}
func assertNoTrust(t *testing.T, peers [2]*testPeer, wait time.Duration) {
	t.Helper()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case event := <-peers[0].events:
			if event.Kind == "trusted" || event.Kind == "sharing" {
				t.Fatal("input/trust before both approvals")
			}
		case event := <-peers[1].events:
			if event.Kind == "trusted" || event.Kind == "sharing" {
				t.Fatal("input/trust before both approvals")
			}
		case <-timer.C:
			return
		}
	}
}
func TestAutoPairMutualApprovalAndAutomaticReconnect(t *testing.T) {
	keys := [2][]byte{identity(t), identity(t)}
	peers := startPeers(t, keys, [2]map[string]string{})
	first, second := await(t, peers[0], "confirm"), await(t, peers[1], "confirm")
	if first.Code != second.Code || len(first.Code) != 8 || first.Token != second.Token {
		t.Fatal("TLS channel confirmation codes disagree")
	}
	assertNoTrust(t, peers, 100*time.Millisecond)
	peers[0].service.Command(Command{Kind: "approve", Token: first.Token})
	assertNoTrust(t, peers, 100*time.Millisecond)
	peers[1].service.Command(Command{Kind: "approve", Token: second.Token})
	a, b := await(t, peers[0], "trusted"), await(t, peers[1], "trusted")
	if a.Key != b.Key || a.ID != peers[1].service.ID || b.ID != peers[0].service.ID {
		t.Fatal("incorrect derived trust")
	}
	if _, err := bridge.ParseKey(a.Key); err != nil {
		t.Fatal(err)
	}
	sa, sb := await(t, peers[0], "sharing"), await(t, peers[1], "sharing")
	if sa.Role == sb.Role {
		t.Fatal("both peers claimed the same role")
	}
	controller := peers[0]
	if sb.Role == "control" {
		controller = peers[1]
	}
	select {
	case <-controller.verified:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic control transport did not exchange heartbeat")
	}
	stopPeers(t, peers)
	peers = startPeers(t, keys, [2]map[string]string{{a.ID: a.Key}, {b.ID: b.Key}})
	for _, p := range peers {
		for {
			event := awaitReconnect(t, p)
			if event.Kind == "sharing" {
				break
			}
		}
	}
	select {
	case <-peers[0].verified:
	case <-peers[1].verified:
	case <-time.After(5 * time.Second):
		t.Fatal("resumed transport did not authenticate")
	}
	peers[0].service.Command(Command{Kind: "settings", Role: "auto", Edge: "right", Enabled: false})
	for _, p := range peers {
		select {
		case <-p.stopped:
		case <-time.After(3 * time.Second):
			t.Fatal("pause did not release sharing")
		}
	}
	peers[0].service.Command(Command{Kind: "settings", Role: "auto", Edge: "right", Enabled: true})
	select {
	case <-peers[0].verified:
	case <-peers[1].verified:
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not reconnect")
	}
}

func TestAutoTLSRejectsAdvertisedIdentityMismatch(t *testing.T) {
	callbacks := func(key []byte) *Service {
		service, err := New(Config{Identity: key, Emit: func(Event) {}, Receive: func(context.Context, net.Listener, []byte) error { return nil }, Control: func(context.Context, string, []byte, string) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	server, client := callbacks(identity(t)), callbacks(identity(t))
	cfg, _ := server.tlsConfig(true, "")
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(2 * time.Second))
		tls.Server(raw, cfg).Handshake()
	}()
	// A forged beacon advertising somebody else's identity must fail before SAS.
	clientCfg, _ := client.tlsConfig(false, client.ID)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", ln.Addr().String(), clientCfg)
	if err == nil {
		conn.Close()
		t.Fatal("certificate did not match advertised identity but was accepted")
	}
	<-done
}
func awaitReconnect(t *testing.T, p *testPeer) Event {
	t.Helper()
	select {
	case event := <-p.events:
		if event.Kind == "confirm" || event.Kind == "error" {
			t.Fatalf("saved trust failed: %s %s", event.Kind, event.Message)
		}
		return event
	case <-time.After(8 * time.Second):
		t.Fatal("trusted peers did not reconnect")
		return Event{}
	}
}
func TestAutoPairRejectsStaleConfirmationAndDenial(t *testing.T) {
	peers := startPeers(t, [2][]byte{identity(t), identity(t)}, [2]map[string]string{})
	a, b := await(t, peers[0], "confirm"), await(t, peers[1], "confirm")
	peers[0].service.Command(Command{Kind: "approve", Token: "old-session-token"})
	peers[1].service.Command(Command{Kind: "approve", Token: b.Token})
	assertNoTrust(t, peers, 150*time.Millisecond)
	peers[0].service.Command(Command{Kind: "reject", Token: a.Token})
	for _, p := range peers {
		await(t, p, "rejected")
	}
	assertNoTrust(t, peers, 100*time.Millisecond)
}
func TestAutoPairRefusesMismatchedSavedSecrets(t *testing.T) {
	keys := [2][]byte{identity(t), identity(t)}
	cfg := func(key []byte) *Service {
		s, err := New(Config{Identity: key, Emit: func(Event) {}, Receive: func(context.Context, net.Listener, []byte) error { return nil }, Control: func(context.Context, string, []byte, string) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	one, two := cfg(keys[0]), cfg(keys[1])
	key1, _ := bridge.NewKey()
	key2, _ := bridge.NewKey()
	peers := startPeers(t, keys, [2]map[string]string{{two.ID: key1}, {one.ID: key2}})
	for _, p := range peers {
		select {
		case event := <-p.events:
			if event.Kind != "error" {
				t.Fatalf("expected authentication failure, got %s", event.Kind)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("secret mismatch not rejected")
		}
	}
	assertNoTrust(t, peers, 100*time.Millisecond)
}
func TestAutoRoleElectionIsSymmetric(t *testing.T) {
	for _, local := range []string{"auto", "control", "receive"} {
		for _, remote := range []string{"auto", "control", "receive"} {
			a, b := resolveRole("a", "b", local, remote), resolveRole("b", "a", remote, local)
			if local == remote && local != "auto" {
				if a != "" || b != "" {
					t.Fatal("conflicting explicit roles allowed")
				}
				continue
			}
			if a == "" || b == "" || a == b {
				t.Fatalf("inconsistent election %s/%s -> %s/%s", local, remote, a, b)
			}
		}
	}
}

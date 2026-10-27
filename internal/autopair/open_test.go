package autopair

import (
	"context"
	"io"
	"mousebridge/internal/bridge"
	"mousebridge/internal/desktop"
	"net"
	"testing"
	"time"
)

type openTestPeer struct {
	service  *OpenService
	events   chan Event
	verified chan string
	cancel   context.CancelFunc
	done     chan error
}

func startOpenPeers(t *testing.T, count int, disabled ...int) []*openTestPeer {
	t.Helper()
	connections, addresses := udpListeners(t, count)
	peers := make([]*openTestPeer, count)
	for i := range peers {
		p := &openTestPeer{events: make(chan Event, 256), verified: make(chan string, 32), done: make(chan error, 1)}
		var lease bridge.InputLease
		targets := make([]string, 0, count-1)
		for j, addr := range addresses {
			if i != j {
				targets = append(targets, addr)
			}
		}
		inputEnabled := len(disabled) == 0 || disabled[0] != i
		cfg := Config{Identity: identity(t), Name: "open-peer", Listen: "127.0.0.1:0", DiscoveryConn: connections[i], Targets: targets, Enabled: inputEnabled,
			Emit: func(event Event) { p.events <- event },
			Receive: func(ctx context.Context, ln net.Listener, key []byte) error {
				return bridge.Serve(ctx, ln, key, func() *bridge.Machine {
					return bridge.NewLeasedMachine(&desktop.Recorder{W: 100, H: 100, Writer: io.Discard}, 100, 100, &lease)
				})
			},
			Control: func(ctx context.Context, addr string, key []byte, edge string) error {
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
					if reply, err := session.Receive(); err != nil || reply.Kind != "pong" {
						return err
					}
					if !verified {
						p.verified <- addr
						verified = true
					}
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(40 * time.Millisecond):
					}
				}
			}}
		service, err := NewOpen(cfg, true)
		if err != nil {
			t.Fatal(err)
		}
		p.service = service
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		peers[i] = p
		go func() { p.done <- service.Run(ctx) }()
	}
	t.Cleanup(func() {
		for _, p := range peers {
			p.cancel()
		}
		for _, p := range peers {
			select {
			case err := <-p.done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("open service did not release on shutdown")
			}
		}
	})
	return peers
}
func openEvent(t *testing.T, p *openTestPeer, kind string, predicate func(Event) bool) Event {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-p.events:
			if event.Kind == "confirm" || event.Kind == "trusted" {
				t.Fatal("open mode requested approval or persisted trust")
			}
			if event.Kind == "error" {
				t.Fatalf("open mode error: %s", event.Message)
			}
			if event.Kind == kind && (predicate == nil || predicate(event)) {
				return event
			}
		case <-timer.C:
			t.Fatalf("timeout waiting for %s", kind)
		}
	}
}
func openVerified(t *testing.T, p *openTestPeer) string {
	t.Helper()
	select {
	case addr := <-p.verified:
		return addr
	case <-time.After(5 * time.Second):
		t.Fatal("bidirectional TLS input link did not authenticate")
		return ""
	}
}
func TestOpenLANThreePeersNoApprovalBidirectionalAndSwitch(t *testing.T) {
	peers := startOpenPeers(t, 3)
	var roster Event
	for i, p := range peers {
		event := openEvent(t, p, "devices", func(e Event) bool { return len(e.Peers) == 2 })
		if i == 0 {
			roster = event
		}
		openEvent(t, p, "receiving", nil)
		openVerified(t, p)
	}
	// Every host is simultaneously a receiver and a controller. Changing the
	// selected target must authenticate a new input link without numeric approval.
	var target string
	for _, device := range roster.Peers {
		if device.ID != roster.Selected {
			target = device.ID
		}
	}
	peers[0].service.Command(Command{Kind: "select", ID: target})
	openEvent(t, peers[0], "controlling", func(e Event) bool { return e.ID == target })
	openVerified(t, peers[0])
	// Closing discovery revokes all links, not just the selected outgoing target.
	peers[1].service.Command(Command{Kind: "settings", Discover: false, Enabled: false, Edge: "right"})
	openEvent(t, peers[1], "devices", func(e Event) bool { return len(e.Peers) == 0 })
	for i, p := range peers {
		if i != 1 {
			openEvent(t, p, "devices", func(e Event) bool { return len(e.Peers) == 1 })
		}
	}
	peers[1].service.Command(Command{Kind: "settings", Discover: true, Enabled: true, Edge: "right"})
	for _, p := range peers {
		openEvent(t, p, "devices", func(e Event) bool { return len(e.Peers) == 2 })
	}
	openVerified(t, peers[1])
}

func TestOpenLANDiscoveryWithoutInputPermissionDoesNotShare(t *testing.T) {
	peers := startOpenPeers(t, 2, 0)
	for _, p := range peers {
		openEvent(t, p, "devices", func(e Event) bool { return len(e.Peers) == 1 })
	}
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-peers[0].verified:
		t.Fatal("disabled input still started control")
	case <-peers[1].verified:
		t.Fatal("remote input started before both sides enabled")
	case <-timer.C:
	}
	peers[0].service.Command(Command{Kind: "settings", Discover: true, Enabled: true, Edge: "left"})
	for _, p := range peers {
		openVerified(t, p)
	}
}

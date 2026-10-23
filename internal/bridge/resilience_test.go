package bridge_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"mousebridge/internal/bridge"
	"net"
	"strings"
	"testing"
	"time"
)

func receiver(t *testing.T) (string, []byte, eventSink) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	key[0] = 17
	sink := make(eventSink, 128)
	done := make(chan error, 1)
	go func() {
		done <- bridge.Serve(ctx, ln, key, func() *bridge.Machine { return bridge.NewMachine(sink, 800, 600) })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not stop")
		}
	})
	return ln.Addr().String(), key, sink
}
func exchange(t *testing.T, c *bridge.Session, e bridge.Event) bridge.Event {
	t.Helper()
	if err := c.Send(e); err != nil {
		t.Fatal(err)
	}
	r, err := c.Receive()
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestIdlePeerTimeoutReleasesAllButtons(t *testing.T) {
	addr, key, sink := receiver(t)
	c, err := bridge.Dial(context.Background(), addr, key)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exchange(t, c, bridge.Event{Kind: "enter", Edge: "left", Y: 1})
	for b := 0; b < 3; b++ {
		exchange(t, c, bridge.Event{Kind: "button", Button: b, Down: true})
	}
	for i := 0; i < 4; i++ {
		<-sink
	}
	deadline := time.After(bridge.SessionTimeout + time.Second)
	for i := 0; i < 3; i++ {
		select {
		case e := <-sink:
			if e.Kind != "button" || e.Down {
				t.Fatalf("bad release: %+v", e)
			}
		case <-deadline:
			t.Fatal("silent peer retained buttons")
		}
	}
}
func TestReceiverRejectsSecondController(t *testing.T) {
	addr, key, _ := receiver(t)
	c, err := bridge.Dial(context.Background(), addr, key)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	other, err := bridge.Dial(context.Background(), addr, key)
	if err == nil {
		other.Close()
		t.Fatal("two controllers own receiver")
	}
	if !strings.Contains(err.Error(), "busy") {
		t.Fatalf("unexpected rejection: %v", err)
	}
}
func TestMalformedOrOversizedFrameReleasesButtons(t *testing.T) {
	for _, bad := range []string{"{\"kind\":\"unknown\"}\n", "{\"kind\":\"ping\",\"unexpected\":1}\n", strings.Repeat("x", bridge.MaxFrame+1) + "\n"} {
		t.Run(fmt.Sprint(len(bad)), func(t *testing.T) {
			addr, key, sink := receiver(t)
			cfg, _ := bridge.TLSConfig(key, false)
			c, err := tls.Dial("tcp", addr, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(3 * time.Second))
			r := bufio.NewReader(c)
			if _, err = r.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			for _, msg := range []string{"{\"kind\":\"enter\",\"edge\":\"right\"}\n", "{\"kind\":\"button\",\"down\":true}\n"} {
				if _, err = c.Write([]byte(msg)); err != nil {
					t.Fatal(err)
				}
				if _, err = r.ReadString('\n'); err != nil {
					t.Fatal(err)
				}
			}
			<-sink
			<-sink
			c.Write([]byte(bad))
			select {
			case e := <-sink:
				if e.Kind != "button" || e.Down {
					t.Fatalf("not released: %+v", e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("protocol failure did not release")
			}
		})
	}
}
func TestLeftEntryClampsAndIgnoresInFlightEventsAfterReturn(t *testing.T) {
	s := &screen{}
	m := bridge.NewMachine(s, 800, 600)
	m.Handle(bridge.Event{Kind: "enter", Edge: "left", Y: 1})
	if e := s.events[0]; e.X != 798 || e.Y != 599 {
		t.Fatalf("left entry %+v", e)
	}
	m.Handle(bridge.Event{Kind: "move", X: -100, Y: 1000})
	if e := s.events[1]; e.X != 698 || e.Y != 599 {
		t.Fatalf("clamp %+v", e)
	}
	r, _ := m.Handle(bridge.Event{Kind: "move", X: 200})
	if r.Kind != "return" {
		t.Fatal("did not return")
	}
	before := len(s.events)
	m.Handle(bridge.Event{Kind: "button", Down: true})
	m.Handle(bridge.Event{Kind: "move", X: 3})
	m.Handle(bridge.Event{Kind: "wheel", Y: 1})
	if len(s.events) != before {
		t.Fatal("late event injected after return")
	}
}
func TestInvalidInputsNeverReachDesktop(t *testing.T) {
	for _, e := range []bridge.Event{{Kind: "enter", Edge: "top"}, {Kind: "enter", Edge: "right", Y: 2}, {Kind: "move", X: math.NaN()}, {Kind: "button", Button: 3}, {Kind: "wheel", Y: 1000}} {
		s := &screen{}
		m := bridge.NewMachine(s, 800, 600)
		if _, err := m.Handle(e); err == nil {
			t.Fatalf("accepted %+v", e)
		}
		if len(s.events) > 0 {
			t.Fatal("invalid event reached desktop")
		}
	}
}
func TestQueueFailureRestoresLocalControl(t *testing.T) {
	calls := 0
	var warps [][2]float64
	c := bridge.NewController(800, 600, "left", func(e bridge.Event) bool { calls++; return calls == 1 }, func(x, y float64) { warps = append(warps, [2]float64{x, y}) })
	c.Handle(bridge.Sample{Kind: "move", X: 0, Y: 300, DX: -1})
	c.Handle(bridge.Sample{Kind: "move", DX: -2})
	if len(warps) != 2 || warps[1][0] != 3 {
		t.Fatalf("not restored %+v", warps)
	}
	if c.Handle(bridge.Sample{Kind: "button", Down: true}) {
		t.Fatal("local button still suppressed")
	}
}

func TestClosingListenerTerminatesActiveSessions(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := make([]byte, 32)
	done := make(chan error, 1)
	go func() {
		done <- bridge.Serve(ctx, ln, key, func() *bridge.Machine { return bridge.NewMachine(make(eventSink, 16), 800, 600) })
	}()
	c, err := bridge.Dial(ctx, ln.Addr().String(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ln.Close()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("server stuck waiting for active session after listener closed")
	}
}

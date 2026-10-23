package bridge_test

import (
	"context"
	"mousebridge/internal/bridge"
	"net"
	"testing"
	"time"
)

type eventSink chan bridge.Event

func (s eventSink) Apply(e bridge.Event) error { s <- e; return nil }
func TestNetworkSessionInjectsAndReleasesOnDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	key[0] = 9
	sink := make(eventSink, 16)
	go bridge.Serve(ctx, ln, key, func() *bridge.Machine { return bridge.NewMachine(sink, 1920, 1080) })
	c, err := bridge.Dial(ctx, ln.Addr().String(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Send(bridge.Event{Kind: "enter", Y: 0.5, Edge: "right"}); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Receive(); err != nil || r.Kind != "entered" {
		t.Fatalf("entry ack %v %v", r, err)
	}
	c.Send(bridge.Event{Kind: "button", Button: 0, Down: true})
	c.Close()
	want := []string{"move", "button", "button"}
	for i, kind := range want {
		select {
		case e := <-sink:
			if e.Kind != kind || (i == 2 && e.Down) {
				t.Fatalf("event %d: %+v", i, e)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("disconnect left input held")
		}
	}
}

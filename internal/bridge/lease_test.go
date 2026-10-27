package bridge_test

import (
	"mousebridge/internal/bridge"
	"testing"
)

type leaseSink struct{ events []bridge.Event }

func (s *leaseSink) Apply(event bridge.Event) error { s.events = append(s.events, event); return nil }

func TestInputLeaseSerializesPeersAndReleasesButtonsBeforeHandoff(t *testing.T) {
	var lease bridge.InputLease
	sink := &leaseSink{}
	first := bridge.NewLeasedMachine(sink, 100, 100, &lease)
	second := bridge.NewLeasedMachine(sink, 100, 100, &lease)
	enter := bridge.Event{Kind: "enter", Edge: "right", Y: 0.5}
	if reply, err := first.Handle(enter); err != nil || reply.Kind != "entered" {
		t.Fatal(reply, err)
	}
	first.Handle(bridge.Event{Kind: "button", Button: 0, Down: true})
	before := len(sink.events)
	if reply, err := second.Handle(enter); err != nil || reply.Kind != "return" {
		t.Fatal("busy desktop did not return second mouse", reply, err)
	}
	if len(sink.events) != before {
		t.Fatal("competing peer injected input")
	}
	// Releasing a rejected/idle session must not steal the active owner's lease.
	second.Release()
	if reply, _ := second.Handle(enter); reply.Kind != "return" {
		t.Fatal("non-owner release revoked active peer")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if last := sink.events[len(sink.events)-1]; last.Kind != "button" || last.Down {
		t.Fatal("held button not released before handoff")
	}
	if reply, err := second.Handle(enter); err != nil || reply.Kind != "entered" {
		t.Fatal("released desktop unavailable", reply, err)
	}
	first.Release()
	third := bridge.NewLeasedMachine(sink, 100, 100, &lease)
	if reply, _ := third.Handle(enter); reply.Kind != "return" {
		t.Fatal("old owner's second release revoked new owner")
	}
	second.Release()
}

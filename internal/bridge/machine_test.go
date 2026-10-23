package bridge_test

import (
	"mousebridge/internal/bridge"
	"testing"
)

type screen struct{ events []bridge.Event }

func (s *screen) Apply(e bridge.Event) error { s.events = append(s.events, e); return nil }
func TestRemoteMouseCrossesScreenAndReleasesDrag(t *testing.T) {
	s := &screen{}
	m := bridge.NewMachine(s, 1920, 1080)
	if _, err := m.Handle(bridge.Event{Kind: "enter", Y: 0.5, Edge: "right"}); err != nil {
		t.Fatal(err)
	}
	if got := s.events[0]; got.X != 1 || got.Y != 540 {
		t.Fatalf("entry: %+v", got)
	}
	m.Handle(bridge.Event{Kind: "button", Button: 0, Down: true})
	m.Handle(bridge.Event{Kind: "move", X: 100, Y: 20})
	if got := s.events[len(s.events)-1]; got.X != 101 || got.Y != 560 {
		t.Fatalf("movement: %+v", got)
	}
	reply, err := m.Handle(bridge.Event{Kind: "move", X: -200})
	if err != nil || reply.Kind != "return" {
		t.Fatalf("return: %+v %v", reply, err)
	}
	if last := s.events[len(s.events)-1]; last.Kind != "button" || last.Down {
		t.Fatalf("stuck button: %+v", last)
	}
}

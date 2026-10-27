package bridge_test

import (
	"mousebridge/internal/bridge"
	"testing"
)

func TestControllerDoesNotStealLocalDragAndEscapeReturns(t *testing.T) {
	var sent []bridge.Event
	var warps [][2]float64
	c := bridge.NewController(1920, 1080, "right", func(e bridge.Event) bool { sent = append(sent, e); return true }, func(x, y float64) { warps = append(warps, [2]float64{x, y}) })
	c.Handle(bridge.Sample{Kind: "button", Button: 0, Down: true})
	if c.Handle(bridge.Sample{Kind: "move", X: 1919, Y: 540, DX: 5}) {
		t.Fatal("stole local drag")
	}
	c.Handle(bridge.Sample{Kind: "button", Button: 0, Down: false})
	if !c.Handle(bridge.Sample{Kind: "move", X: 1919, Y: 540, DX: 5}) || sent[0].Kind != "enter" {
		t.Fatal("did not cross edge")
	}
	if !c.Handle(bridge.Sample{Kind: "move", DX: 12, DY: -3}) || sent[1].X != 12 {
		t.Fatal("did not forward delta")
	}
	if !c.Handle(bridge.Sample{Kind: "escape"}) {
		t.Fatal("escape not handled")
	}
	if sent[2].Kind != "leave" || len(warps) != 2 || warps[1][0] != 1916 {
		t.Fatalf("return failed: %+v %+v", sent, warps)
	}
}

func TestControllerUsesOuterDesktopEdge(t *testing.T) {
	// Two real local displays: (0,0,1920,1080) and (1920,48,1920,1080).
	// The primary display's right edge and the second display's interior must
	// remain local. Only the full desktop's outside edge may send enter.
	for _, tc := range []struct {
		name, edge string
		x, y, dx   float64
		enter      bool
	}{
		{"primary right edge", "right", 1919, 540, 5, false},
		{"second display middle", "right", 2600, 540, 5, false},
		{"outer right edge", "right", 3839, 540, 5, true},
		{"fractional outer right edge", "right", 3839.75, 540, 5, true},
		{"right edge moving inward", "right", 3839, 540, -5, false},
		{"outside right", "right", 4000, 540, 5, false},
		{"above desktop", "right", 3839, -1, 5, false},
		{"below desktop", "right", 3839, 1128, 5, false},
		{"outer left edge", "left", 0, 540, -5, true},
		{"outside left", "left", -100, 540, -5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []bridge.Event
			c := bridge.NewController(3840, 1128, tc.edge, func(e bridge.Event) bool {
				sent = append(sent, e)
				return true
			}, func(float64, float64) {})
			if got := c.Handle(bridge.Sample{Kind: "move", X: tc.x, Y: tc.y, DX: tc.dx}); got != tc.enter {
				t.Fatalf("captured=%v, want %v; events=%+v", got, tc.enter, sent)
			}
			if !tc.enter && len(sent) != 0 {
				t.Fatalf("local movement sent remote events: %+v", sent)
			}
			if tc.enter && (len(sent) != 1 || sent[0].Kind != "enter") {
				t.Fatalf("missing enter: %+v", sent)
			}
		})
	}
}

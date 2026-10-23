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

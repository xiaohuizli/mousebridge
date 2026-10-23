package bridge

import (
	"sync"
	"time"
)

// Sample contains primary-screen coordinates plus physical relative movement.
type Sample struct {
	Kind         string
	X, Y, DX, DY float64
	Button       int
	Down         bool
}
type Controller struct {
	mu            sync.Mutex
	width, height float64
	edge          string
	send          func(Event) bool
	warp          func(float64, float64)
	remote        bool
	held          [3]bool
	y             float64
	cooldown      time.Time
}

func NewController(w, h int, edge string, send func(Event) bool, warp func(float64, float64)) *Controller {
	return &Controller{width: float64(w), height: float64(h), edge: edge, send: send, warp: warp}
}

// Handle runs on the OS hook thread. send MUST be nonblocking; queue overflow
// cancels the connection. warp must not synchronously re-enter Handle.
func (c *Controller) Handle(s Sample) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.Kind == "escape" {
		if !c.remote {
			return false
		}
		c.send(Event{Kind: "leave"})
		c.release(c.y)
		return true
	}
	if !c.remote {
		if s.Kind == "button" && s.Button >= 0 && s.Button < 3 {
			c.held[s.Button] = s.Down
		}
		dragging := c.held[0] || c.held[1] || c.held[2]
		atEdge := (c.edge == "right" && s.X >= c.width-1 && s.DX > 0) || (c.edge == "left" && s.X <= 0 && s.DX < 0)
		if s.Kind == "move" && atEdge && !dragging && time.Now().After(c.cooldown) {
			c.y = clamp(s.Y/(c.height-1), 0, 1)
			if !c.send(Event{Kind: "enter", Y: c.y, Edge: c.edge}) {
				return false
			}
			c.remote = true
			c.warp(c.width/2, c.height/2)
			return true
		}
		return false
	}
	e := Event{Kind: s.Kind, Button: s.Button, Down: s.Down}
	switch s.Kind {
	case "move":
		e.X = s.DX
		e.Y = s.DY
	case "wheel":
		e.X = s.DX
		e.Y = s.DY
	case "button":
	default:
		return false
	}
	if !c.send(e) {
		c.release(c.y)
	}
	return true
}
func (c *Controller) release(y float64) {
	if !c.remote {
		return
	}
	c.remote = false
	c.held = [3]bool{}
	c.cooldown = time.Now().Add(300 * time.Millisecond)
	x := c.width - 4
	if c.edge == "left" {
		x = 3
	}
	c.warp(x, clamp(y, 0, 1)*(c.height-1))
}
func (c *Controller) Return(y float64) { c.mu.Lock(); defer c.mu.Unlock(); c.release(y) }
func (c *Controller) Disconnect()      { c.mu.Lock(); defer c.mu.Unlock(); c.release(c.y) }

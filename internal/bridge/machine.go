// Package bridge implements MouseBridge's transport-independent mouse protocol.
package bridge

import (
	"errors"
	"math"
	"sync"
)

// Event is a version-1 command. Move is relative on the wire and absolute at Sink.
// Enter.Y is normalized [0,1]; wheel values are in detents (positive = up/right).
// Buttons are 0=left, 1=right, 2=middle. No keyboard content is transmitted.
type Event struct {
	Kind   string  `json:"kind"`
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Button int     `json:"button,omitempty"`
	Down   bool    `json:"down,omitempty"`
	Edge   string  `json:"edge,omitempty"`
}

type Sink interface{ Apply(Event) error }

type Machine struct {
	sink                Sink
	width, height, x, y float64
	active              bool
	edge                string
	held                [3]bool
	lease               *InputLease
}

// InputLease gives one remote mouse at a time ownership of this desktop, while
// other connected peers remain idle and can enter as soon as it is released.
type InputLease struct {
	mu    sync.Mutex
	owner *Machine
}

func NewLeasedMachine(s Sink, w, h int, lease *InputLease) *Machine {
	m := NewMachine(s, w, h)
	m.lease = lease
	return m
}

func (m *Machine) acquire() bool {
	if m.lease == nil {
		return true
	}
	m.lease.mu.Lock()
	defer m.lease.mu.Unlock()
	if m.lease.owner != nil && m.lease.owner != m {
		return false
	}
	m.lease.owner = m
	return true
}

func NewMachine(s Sink, w, h int) *Machine {
	return &Machine{sink: s, width: float64(w), height: float64(h)}
}
func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
func (m *Machine) Handle(e Event) (Event, error) {
	if math.IsNaN(e.X) || math.IsNaN(e.Y) || math.IsInf(e.X, 0) || math.IsInf(e.Y, 0) {
		return Event{}, errors.New("non-finite coordinates")
	}
	switch e.Kind {
	case "ping":
		return Event{Kind: "pong"}, nil
	case "enter":
		if m.active || (e.Edge != "left" && e.Edge != "right") || e.Y < 0 || e.Y > 1 || m.width < 4 || m.height < 4 {
			return Event{}, errors.New("invalid entry")
		}
		if !m.acquire() {
			return Event{Kind: "return", Y: e.Y}, nil
		}
		m.edge = e.Edge
		m.y = math.Round(e.Y * (m.height - 1))
		m.x = 1
		if e.Edge == "left" {
			m.x = m.width - 2
		}
		m.active = true
		return Event{Kind: "entered"}, m.sink.Apply(Event{Kind: "move", X: m.x, Y: m.y})
	case "leave":
		return Event{Kind: "return"}, m.Release()
	case "move":
		if !m.active {
			return Event{Kind: "idle"}, nil
		}
		if math.Abs(e.X) > 32768 || math.Abs(e.Y) > 32768 {
			return Event{}, errors.New("movement out of bounds")
		}
		m.x += e.X
		m.y = clamp(m.y+e.Y, 0, m.height-1)
		if (m.edge == "right" && m.x <= 0) || (m.edge == "left" && m.x >= m.width-1) {
			return Event{Kind: "return", Y: m.y / (m.height - 1)}, m.Release()
		}
		m.x = clamp(m.x, 0, m.width-1)
		return Event{}, m.sink.Apply(Event{Kind: "move", X: m.x, Y: m.y})
	case "button":
		if e.Button < 0 || e.Button > 2 {
			return Event{}, errors.New("invalid button")
		}
		if !m.active {
			return Event{Kind: "idle"}, nil
		}
		m.held[e.Button] = e.Down
		return Event{}, m.sink.Apply(e)
	case "wheel":
		if math.Abs(e.X) > 100 || math.Abs(e.Y) > 100 {
			return Event{}, errors.New("invalid wheel")
		}
		if !m.active {
			return Event{Kind: "idle"}, nil
		}
		return Event{}, m.sink.Apply(e)
	default:
		return Event{}, errors.New("unknown event")
	}
}

// Release is idempotent and is also called on EOF, timeout and protocol failure.
func (m *Machine) Release() error {
	m.active = false
	var err error
	for b, down := range m.held {
		if down {
			err = errors.Join(err, m.sink.Apply(Event{Kind: "button", Button: b, Down: false}))
			m.held[b] = false
		}
	}
	if m.lease != nil {
		m.lease.mu.Lock()
		if m.lease.owner == m {
			m.lease.owner = nil
		}
		m.lease.mu.Unlock()
	}
	return err
}

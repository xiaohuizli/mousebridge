// Package desktop adapts full-desktop mouse capture/injection to the OS.
package desktop

import (
	"context"
	"encoding/json"
	"io"
	"mousebridge/internal/bridge"
	"sync"
)

type Backend interface {
	bridge.Sink
	Size() (int, int)
	Capture(context.Context, func(bridge.Sample) bool) error
}

// Recorder never calls operating-system input APIs; useful for reproducible runs.
type Recorder struct {
	W, H   int
	Writer io.Writer
	mu     sync.Mutex
}

func (r *Recorder) Size() (int, int) { return r.W, r.H }
func (r *Recorder) Apply(e bridge.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return json.NewEncoder(r.Writer).Encode(e)
}
func (r *Recorder) Capture(ctx context.Context, _ func(bridge.Sample) bool) error {
	<-ctx.Done()
	return ctx.Err()
}

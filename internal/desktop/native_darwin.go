//go:build darwin && cgo

package desktop

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics
#include "native_darwin.h"
*/
import "C"
import (
	"context"
	"fmt"
	"mousebridge/internal/bridge"
	"runtime"
	"sync"
)

type Native struct{ mu sync.Mutex }

// Multiple LAN receivers and the local controller share the native C state.
var nativeApplyMu sync.Mutex

var captureMu sync.Mutex
var captureHandler func(bridge.Sample) bool

func Open() (*Native, error) {
	if C.mb_allowed() == 0 {
		return nil, fmt.Errorf("macOS Accessibility permission required for this terminal/binary; enable it in System Settings, then restart")
	}
	return &Native{}, nil
}
func (n *Native) Size() (int, int) { return int(C.mb_width()), int(C.mb_height()) }
func (n *Native) Apply(e bridge.Event) error {
	nativeApplyMu.Lock()
	defer nativeApplyMu.Unlock()
	k := 0
	switch e.Kind {
	case "move":
		k = 1
	case "button":
		k = 2
	case "wheel":
		k = 3
	default:
		return fmt.Errorf("unsupported native event %q", e.Kind)
	}
	d := 0
	if e.Down {
		d = 1
	}
	if C.mb_apply(C.int(k), C.double(e.X), C.double(e.Y), C.int(e.Button), C.int(d)) != 0 {
		return fmt.Errorf("CoreGraphics event creation failed")
	}
	return nil
}
func (n *Native) Capture(ctx context.Context, fn func(bridge.Sample) bool) error {
	captureMu.Lock()
	defer captureMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	captureHandler = fn
	defer func() { captureHandler = nil }()
	C.mb_reset()
	stop := context.AfterFunc(ctx, func() { C.mb_stop() })
	defer stop()
	if code := C.mb_capture(); code != 0 {
		return fmt.Errorf("event tap failed (%d): enable Accessibility and Input Monitoring, restart terminal", int(code))
	}
	return ctx.Err()
}

//export goMouseEvent
func goMouseEvent(kind C.int, x, y, dx, dy C.double, button, down C.int) C.int {
	k := ""
	switch int(kind) {
	case 1:
		k = "move"
	case 2:
		k = "button"
	case 3:
		k = "wheel"
	case 4:
		k = "escape"
	}
	if captureHandler != nil && captureHandler(bridge.Sample{Kind: k, X: float64(x), Y: float64(y), DX: float64(dx), DY: float64(dy), Button: int(button), Down: down != 0}) {
		return 1
	}
	return 0
}

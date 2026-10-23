//go:build windows && (amd64 || arm64)

package desktop

import (
	"context"
	"fmt"
	"math"
	"mousebridge/internal/bridge"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var sendInput = user32.NewProc("SendInput")
var getMetrics = user32.NewProc("GetSystemMetrics")
var setHook = user32.NewProc("SetWindowsHookExW")
var nextHook = user32.NewProc("CallNextHookEx")
var unhook = user32.NewProc("UnhookWindowsHookEx")
var marker uintptr = 0x4d424731

type Native struct {
	mu   sync.Mutex
	w, h int
}
type point struct{ X, Y int32 }
type mouseHook struct {
	Pt                point
	Data, Flags, Time uint32
	Extra             uintptr
}
type keyHook struct {
	VK, Scan, Flags, Time uint32
	Extra                 uintptr
}
type mouseInput struct {
	DX, DY            int32
	Data, Flags, Time uint32
	Extra             uintptr
}
type input struct {
	Type  uint32
	Pad   uint32
	Mouse mouseInput
}
type message struct {
	Hwnd           uintptr
	Message        uint32
	Wparam, Lparam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}

func Open() (*Native, error) {
	user32.NewProc("SetProcessDPIAware").Call()
	w, _, _ := getMetrics.Call(0)
	h, _, _ := getMetrics.Call(1)
	if w < 4 || h < 4 {
		return nil, fmt.Errorf("no interactive desktop")
	}
	return &Native{w: int(w), h: int(h)}, nil
}
func (n *Native) Size() (int, int) { return n.w, n.h }
func (n *Native) Apply(e bridge.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	var items []input
	add := func(flags uint32, dx, dy int32, data uint32) {
		items = append(items, input{Mouse: mouseInput{DX: dx, DY: dy, Data: data, Flags: flags, Extra: marker}})
	}
	switch e.Kind {
	case "move":
		add(0x8001, int32(math.Round(e.X*65535/float64(n.w-1))), int32(math.Round(e.Y*65535/float64(n.h-1))), 0)
	case "button":
		if e.Button < 0 || e.Button > 2 {
			return fmt.Errorf("invalid button")
		}
		flags := []uint32{2, 8, 32}[e.Button]
		if !e.Down {
			flags *= 2
		}
		add(flags, 0, 0, 0)
	case "wheel":
		if e.Y != 0 {
			add(0x0800, 0, 0, uint32(int32(math.Round(e.Y*120))))
		}
		if e.X != 0 {
			add(0x1000, 0, 0, uint32(int32(math.Round(e.X*120))))
		}
	default:
		return fmt.Errorf("unsupported native event %q", e.Kind)
	}
	if len(items) == 0 {
		return nil
	}
	sent, _, err := sendInput.Call(uintptr(len(items)), uintptr(unsafe.Pointer(&items[0])), unsafe.Sizeof(input{}))
	runtime.KeepAlive(items)
	if sent != uintptr(len(items)) {
		return fmt.Errorf("SendInput injected %d/%d (desktop integrity/UIPI restriction possible): %v", sent, len(items), err)
	}
	return nil
}

var winCaptureMu sync.Mutex

func (n *Native) Capture(ctx context.Context, fn func(bridge.Sample) bool) error {
	winCaptureMu.Lock()
	defer winCaptureMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var prev point
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&prev)))
	mouseCB := syscall.NewCallback(func(code int32, wp uintptr, lp unsafe.Pointer) uintptr {
		next := func() uintptr { v, _, _ := nextHook.Call(0, uintptr(code), wp, uintptr(lp)); return v }
		if code < 0 {
			return next()
		}
		p := (*mouseHook)(lp)
		if p.Flags&1 != 0 {
			prev = p.Pt
			return next()
		}
		s := bridge.Sample{X: float64(p.Pt.X), Y: float64(p.Pt.Y)}
		switch wp {
		case 0x200:
			s.Kind = "move"
			s.DX = float64(p.Pt.X - prev.X)
			s.DY = float64(p.Pt.Y - prev.Y)
		case 0x201, 0x202, 0x204, 0x205, 0x207, 0x208:
			s.Kind = "button"
			s.Down = wp == 0x201 || wp == 0x204 || wp == 0x207
			if wp == 0x204 || wp == 0x205 {
				s.Button = 1
			}
			if wp == 0x207 || wp == 0x208 {
				s.Button = 2
			}
		case 0x20a:
			s.Kind = "wheel"
			s.DY = float64(int16(p.Data>>16)) / 120
		case 0x20e:
			s.Kind = "wheel"
			s.DX = float64(int16(p.Data>>16)) / 120
		default:
			return next()
		}
		if fn(s) {
			return 1
		}
		if s.Kind == "move" {
			prev = p.Pt
		}
		return next()
	})
	keyCB := syscall.NewCallback(func(code int32, wp uintptr, lp unsafe.Pointer) uintptr {
		if code >= 0 && (wp == 0x100 || wp == 0x104) {
			p := (*keyHook)(lp)
			if p.VK == 27 && p.Flags&0x10 == 0 && fn(bridge.Sample{Kind: "escape"}) {
				return 1
			}
		}
		v, _, _ := nextHook.Call(0, uintptr(code), wp, uintptr(lp))
		return v
	})
	module, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	mh, _, err := setHook.Call(14, mouseCB, module, 0)
	if mh == 0 {
		return fmt.Errorf("mouse hook: %v", err)
	}
	defer unhook.Call(mh)
	kh, _, err := setHook.Call(13, keyCB, module, 0)
	if kh == 0 {
		return fmt.Errorf("escape hook: %v", err)
	}
	defer unhook.Call(kh)
	var msg message
	user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 0)
	tid, _, _ := kernel32.NewProc("GetCurrentThreadId").Call()
	stop := context.AfterFunc(ctx, func() { user32.NewProc("PostThreadMessageW").Call(tid, 0x12, 0, 0) })
	defer stop()
	for {
		r, _, e := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return e
		}
		if r == 0 {
			break
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	return ctx.Err()
}

//go:build (!darwin && !windows) || (darwin && !cgo) || (windows && !amd64 && !arm64)

package desktop

import (
	"context"
	"errors"
	"mousebridge/internal/bridge"
)

type Native struct{}

func Open() (*Native, error) {
	return nil, errors.New("native input requires macOS with cgo or 64-bit Windows")
}
func (*Native) Size() (int, int)         { return 0, 0 }
func (*Native) Apply(bridge.Event) error { return errors.New("native input unsupported") }
func (*Native) Capture(context.Context, func(bridge.Sample) bool) error {
	return errors.New("native input unsupported")
}

package bridge_test

import (
	"crypto/tls"
	"mousebridge/internal/bridge"
	"net"
	"testing"
	"time"
)

func TestPairKeyAuthenticatesBothEnds(t *testing.T) {
	good := make([]byte, 32)
	good[0] = 42
	bad := make([]byte, 32)
	bad[0] = 43
	for _, tc := range []struct {
		name string
		key  []byte
		ok   bool
	}{{"same key", good, true}, {"wrong key", bad, false}} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			a.SetDeadline(time.Now().Add(time.Second))
			b.SetDeadline(time.Now().Add(time.Second))
			sc, _ := bridge.TLSConfig(good, true)
			cc, _ := bridge.TLSConfig(tc.key, false)
			done := make(chan error, 1)
			go func() { done <- tls.Server(a, sc).Handshake() }()
			ce := tls.Client(b, cc).Handshake()
			if ce != nil {
				b.Close()
			}
			se := <-done
			if tc.ok && (ce != nil || se != nil) {
				t.Fatalf("handshake %v / %v", ce, se)
			}
			if !tc.ok && ce == nil && se == nil {
				t.Fatal("wrong pair key accepted")
			}
		})
	}
}

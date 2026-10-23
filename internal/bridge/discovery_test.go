package bridge_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"mousebridge/internal/bridge"
	"net"
	"testing"
	"time"
)

func TestDiscoveryFindsOnlyPairedReceiver(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 1
	u, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Advertise(ctx, u, key, "test-screen", 43210)
	found, err := bridge.Discover(ctx, u.LocalAddr().String(), key, 100*time.Millisecond)
	if err != nil || len(found) != 1 || found[0].Name != "test-screen" || found[0].Address != "127.0.0.1:43210" {
		t.Fatalf("discovery: %+v %v", found, err)
	}
	wrong := make([]byte, 32)
	found, err = bridge.Discover(ctx, u.LocalAddr().String(), wrong, 100*time.Millisecond)
	if err != nil || len(found) != 0 {
		t.Fatalf("unpaired receiver exposed: %+v %v", found, err)
	}
}

func TestDiscoveryRejectsForgedStaleAndReplayedReplies(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 2
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 2048)
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var request struct {
			Payload struct {
				Nonce string `json:"nonce"`
			} `json:"payload"`
		}
		if json.Unmarshal(buf[:n], &request) != nil {
			return
		}
		for _, tc := range []struct {
			name, nonce string
			stamp       int64
			forged      bool
		}{
			{"forged", request.Payload.Nonce, time.Now().Unix(), true},
			{"expired", request.Payload.Nonce, time.Now().Add(-time.Minute).Unix(), false},
			{"replayed", "00000000000000000000000000000000", time.Now().Unix(), false},
			{"valid", request.Payload.Nonce, time.Now().Unix(), false},
		} {
			payload, _ := json.Marshal(map[string]any{"v": 1, "kind": "receiver", "nonce": tc.nonce, "time": tc.stamp, "name": tc.name, "port": 12345})
			h := hmac.New(sha256.New, key)
			h.Write([]byte("mousebridge/v1/discovery:"))
			h.Write(payload)
			tag := h.Sum(nil)
			if tc.forged {
				tag[0] ^= 255
			}
			packet, _ := json.Marshal(struct {
				Payload json.RawMessage `json:"payload"`
				MAC     []byte          `json:"mac"`
			}{payload, tag})
			conn.WriteTo(packet, addr)
		}
	}()
	peers, err := bridge.Discover(context.Background(), conn.LocalAddr().String(), key, 100*time.Millisecond)
	if err != nil || len(peers) != 1 || peers[0].Name != "valid" {
		t.Fatalf("accepted invalid discovery: %+v %v", peers, err)
	}
}

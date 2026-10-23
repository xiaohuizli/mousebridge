package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"time"
)

const DiscoveryPort = 42047
const ControlPort = 42048

type Peer struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}
type announcement struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	Nonce   string `json:"nonce"`
	Time    int64  `json:"time"`
	Name    string `json:"name,omitempty"`
	Port    int    `json:"port,omitempty"`
}
type envelope struct {
	Payload json.RawMessage `json:"payload"`
	MAC     []byte          `json:"mac"`
}

func signed(key []byte, a announcement) []byte {
	p, _ := json.Marshal(a)
	b, _ := json.Marshal(envelope{p, mac(key, append([]byte("mousebridge/v1/discovery:"), p...))})
	return b
}
func verified(key, b []byte) (announcement, bool) {
	var e envelope
	var a announcement
	if len(b) > 2048 || json.Unmarshal(b, &e) != nil {
		return a, false
	}
	if !hmac.Equal(e.MAC, mac(key, append([]byte("mousebridge/v1/discovery:"), e.Payload...))) {
		return a, false
	}
	if json.Unmarshal(e.Payload, &a) != nil || a.Version != 1 || len(a.Nonce) != 32 || len(a.Name) > 128 {
		return a, false
	}
	age := time.Now().Unix() - a.Time
	return a, age >= -30 && age <= 30
}

// Advertise answers signed discovery requests. The caller owns conn.
func Advertise(ctx context.Context, conn net.PacketConn, key []byte, name string, port int) error {
	if len(name) > 128 {
		name = name[:128]
	}
	buf := make([]byte, 2049)
	for ctx.Err() == nil {
		conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			return err
		}
		a, ok := verified(key, buf[:n])
		if !ok || a.Kind != "discover" {
			continue
		}
		conn.WriteTo(signed(key, announcement{Version: 1, Kind: "receiver", Nonce: a.Nonce, Time: time.Now().Unix(), Name: name, Port: port}), addr)
	}
	return ctx.Err()
}

// Discover accepts only replies bound to this request's random nonce. target
// defaults to IPv4 broadcast; a subnet broadcast or loopback target may be used.
func Discover(ctx context.Context, target string, key []byte, wait time.Duration) ([]Peer, error) {
	addr, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(nonce)
	if _, err = conn.WriteToUDP(signed(key, announcement{Version: 1, Kind: "discover", Nonce: token, Time: time.Now().Unix()}), addr); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetReadDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	peers := map[string]Peer{}
	buf := make([]byte, 2049)
	for len(peers) < 32 {
		n, from, e := conn.ReadFromUDP(buf)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ne, ok := e.(net.Error); ok && ne.Timeout() {
				break
			}
			return nil, e
		}
		a, ok := verified(key, buf[:n])
		if !ok || a.Kind != "receiver" || a.Nonce != token || a.Port < 1 || a.Port > 65535 {
			continue
		}
		address := net.JoinHostPort(from.IP.String(), strconv.Itoa(a.Port))
		peers[address] = Peer{a.Name, address}
	}
	result := make([]Peer, 0, len(peers))
	for _, p := range peers {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result, nil
}

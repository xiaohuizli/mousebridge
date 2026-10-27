package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mousebridge/internal/bridge"
	"mousebridge/internal/desktop"
	"net"
	"os"
	"os/signal"
	"strconv"
	"time"
)

const version = "0.3.1"

func main() {
	log.SetFlags(log.Ltime)
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return usage()
	}
	cmd := os.Args[1]
	if cmd == "help" || cmd == "--help" {
		return usage()
	}
	if cmd == "version" {
		fmt.Println("MouseBridge", version)
		return nil
	}
	if cmd == "keygen" {
		key, err := bridge.NewKey()
		if err == nil {
			fmt.Println(key)
		}
		return err
	}
	key, err := bridge.ParseKey(os.Getenv("MOUSEBRIDGE_KEY"))
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	switch cmd {
	case "auto":
		return automatic(ctx, key, os.Args[2:])
	case "discover":
		return discover(ctx, key, os.Args[2:])
	case "receive":
		return receive(ctx, key, os.Args[2:])
	case "control":
		return control(ctx, key, os.Args[2:])
	case "send":
		return scripted(ctx, key, os.Args[2:])
	default:
		return fmt.Errorf("unknown command %q; use help", cmd)
	}
}
func usage() error {
	fmt.Print(`MouseBridge 0.3.1 — Windows / macOS multi-display mouse sharing

keygen                         Generate a random pairing key; set MOUSEBRIDGE_KEY on both hosts.
auto [-live]                   Auto discover/pair; JSON commands on stdin, events on stdout.
auto -open-lan [-live]         Open LAN multi-peer sharing; no approval/allowlist.
discover [-target IP:42047]     Find receivers paired with that key (IPv4 broadcast).
receive [-listen :42048]        Dry run: log received input as JSON; does not inject.
receive -live                  Enable real system mouse injection.
control -peer IP:42048 -live    Capture mouse; right edge crosses to the remote screen.
        [-edge left|right]     Escape or remote return edge restores local control.
send -peer IP:42048 -script FILE  Send JSONL events; use only a dry-run receiver for testing.
version                        Print version.

Only three mouse buttons and vertical/horizontal wheel are forwarded.
Keyboard, clipboard, files and multi-monitor layouts are outside v0.1.
Use Ctrl+C to stop. Pair key is read only from MOUSEBRIDGE_KEY.
`)
	return nil
}
func discover(ctx context.Context, key []byte, args []string) error {
	f := flag.NewFlagSet("discover", flag.ContinueOnError)
	target := f.String("target", "255.255.255.255:42047", "broadcast or receiver address")
	if err := f.Parse(args); err != nil {
		return err
	}
	peers, err := bridge.Discover(ctx, *target, key, 1200*time.Millisecond)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(peers)
}
func receive(ctx context.Context, key []byte, args []string) error {
	f := flag.NewFlagSet("receive", flag.ContinueOnError)
	addr := f.String("listen", ":42048", "TCP listen address")
	udp := f.String("discovery-listen", ":42047", "UDP discovery address")
	noDiscovery := f.Bool("no-discovery", false, "disable broadcast discovery")
	live := f.Bool("live", false, "inject into OS")
	name := f.String("name", "", "advertised receiver name")
	w := f.Int("width", 1920, "dry-run width")
	h := f.Int("height", 1080, "dry-run height")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *w < 4 || *h < 4 {
		return errors.New("screen dimensions must be >=4")
	}
	var backend desktop.Backend = &desktop.Recorder{W: *w, H: *h, Writer: os.Stdout}
	if *live {
		n, err := desktop.Open()
		if err != nil {
			return err
		}
		backend = n
	}
	width, height := backend.Size()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	if !*noDiscovery {
		conn, err := net.ListenPacket("udp4", *udp)
		if err != nil {
			return err
		}
		defer conn.Close()
		if *name == "" {
			*name, _ = os.Hostname()
		}
		port := ln.Addr().(*net.TCPAddr).Port
		go func() {
			if err := bridge.Advertise(ctx, conn, key, *name, port); err != nil && ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				log.Printf("discovery stopped: %v", err)
			}
		}()
	}
	log.Printf("receiver %s live=%t screen=%dx%d", ln.Addr(), *live, width, height)
	return bridge.Serve(ctx, ln, key, func() *bridge.Machine { return bridge.NewMachine(backend, width, height) })
}
func control(parent context.Context, key []byte, args []string) error {
	f := flag.NewFlagSet("control", flag.ContinueOnError)
	peer := f.String("peer", "", "receiver IP:port")
	edge := f.String("edge", "right", "left or right")
	live := f.Bool("live", false, "enable OS capture")
	if err := f.Parse(args); err != nil {
		return err
	}
	if !*live {
		return errors.New("control requires -live; use send with a dry-run receiver for tests")
	}
	if *peer == "" {
		return errors.New("set -peer using an address from discover")
	}
	if *edge != "left" && *edge != "right" {
		return errors.New("edge must be left or right")
	}
	native, err := desktop.Open()
	if err != nil {
		return err
	}
	session, err := bridge.Dial(parent, *peer, key)
	if err != nil {
		return err
	}
	defer session.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	errs := make(chan error, 1)
	fail := func(err error) {
		select {
		case errs <- err:
		default:
		}
		cancel()
	}
	events := make(chan bridge.Event, 256)
	w, h := native.Size()
	c := bridge.NewController(w, h, *edge, func(e bridge.Event) bool {
		if ctx.Err() != nil {
			return false
		}
		select {
		case events <- e:
			return true
		default:
			fail(errors.New("input queue full; local control restored"))
			return false
		}
	}, func(x, y float64) {
		if err := native.Apply(bridge.Event{Kind: "move", X: x, Y: y}); err != nil {
			fail(err)
		}
	})
	defer c.Disconnect()
	go func() {
		ticker := time.NewTicker(bridge.HeartbeatInterval)
		defer ticker.Stop()
		for {
			var e bridge.Event
			select {
			case <-ctx.Done():
				return
			case e = <-events:
			case <-ticker.C:
				e = bridge.Event{Kind: "ping"}
			}
			if err := session.Send(e); err != nil {
				fail(err)
				return
			}
		}
	}()
	go func() {
		for {
			e, err := session.Receive()
			if err != nil {
				fail(err)
				return
			}
			switch e.Kind {
			case "return":
				c.Return(e.Y)
			case "entered", "ok", "idle", "pong":
			default:
				fail(fmt.Errorf("unexpected response %q", e.Kind))
				return
			}
		}
	}()
	log.Printf("connected to %s; cross %s outer desktop edge; Escape returns; Ctrl+C stops", *peer, *edge)
	err = native.Capture(ctx, c.Handle)
	cancel()
	c.Disconnect()
	if parent.Err() != nil {
		return nil
	}
	select {
	case e := <-errs:
		return e
	default:
	}
	return err
}
func scripted(ctx context.Context, key []byte, args []string) error {
	f := flag.NewFlagSet("send", flag.ContinueOnError)
	peer := f.String("peer", "", "receiver IP:port")
	path := f.String("script", "", "JSONL event file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *peer == "" || *path == "" {
		return errors.New("send requires -peer and -script")
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	s, err := bridge.Dial(ctx, *peer, key)
	if err != nil {
		return err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	d := json.NewDecoder(file)
	d.DisallowUnknownFields()
	n := 0
	for {
		var e bridge.Event
		err = d.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		n++
		if err = s.Send(e); err != nil {
			return err
		}
		r, err := s.Receive()
		if err != nil {
			return fmt.Errorf("event %d: %w", n, err)
		}
		if err = json.NewEncoder(os.Stdout).Encode(r); err != nil {
			return err
		}
	}
	log.Print("sent " + strconv.Itoa(n) + " events")
	return nil
}

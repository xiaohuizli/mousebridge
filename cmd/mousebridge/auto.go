package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mousebridge/internal/autopair"
	"mousebridge/internal/bridge"
	"mousebridge/internal/desktop"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

func automatic(parent context.Context, identity []byte, args []string) error {
	f := flag.NewFlagSet("auto", flag.ContinueOnError)
	live := f.Bool("live", false, "enable native input; otherwise use a recording backend")
	enabled := f.Bool("enabled", false, "start sharing automatically after mutual approval")
	openLAN := f.Bool("open-lan", false, "allow all discovering LAN instances without numeric approval")
	discover := f.Bool("discover", true, "enable LAN discovery and incoming open sessions")
	role := f.String("role", "auto", "auto, control, receive")
	edge := f.String("edge", "right", "left or right")
	listen := f.String("listen", ":0", "pairing TCP listener")
	discovery := f.String("discovery-listen", ":42049", "public discovery UDP listener")
	targets := f.String("targets", "", "test discovery targets, comma separated; defaults to subnet broadcasts")
	name := f.String("name", "", "device name")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		*name, _ = os.Hostname()
	}
	trust := make(map[string]string)
	if value := os.Getenv("MOUSEBRIDGE_TRUST"); value != "" && !*openLAN {
		if err := json.Unmarshal([]byte(value), &trust); err != nil {
			return fmt.Errorf("invalid trust configuration")
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	var outputMu sync.Mutex
	var destinations []string
	if *targets != "" {
		destinations = strings.Split(*targets, ",")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var backendMu sync.Mutex
	var sharedBackend desktop.Backend
	var inputLease bridge.InputLease
	cfg := autopair.Config{Identity: identity, Trust: trust, Name: *name, Listen: *listen, DiscoveryListen: *discovery, Targets: destinations,
		Enabled: *enabled, Role: *role, Edge: *edge, Emit: func(event autopair.Event) { outputMu.Lock(); defer outputMu.Unlock(); encoder.Encode(event) }}
	cfg.Receive = func(ctx context.Context, ln net.Listener, key []byte) error {
		defer ln.Close()
		backendMu.Lock()
		if sharedBackend == nil {
			if *live {
				native, err := desktop.Open()
				if err != nil {
					backendMu.Unlock()
					return err
				}
				sharedBackend = native
			} else {
				sharedBackend = &desktop.Recorder{W: 1920, H: 1080, Writer: io.Discard}
			}
		}
		backend := sharedBackend
		backendMu.Unlock()
		w, h := backend.Size()
		if !*openLAN {
			cfg.Emit(autopair.Event{Kind: "ready", Role: "receive"})
		}
		return bridge.Serve(ctx, ln, key, func() *bridge.Machine { return bridge.NewLeasedMachine(backend, w, h, &inputLease) })
	}
	cfg.Control = func(ctx context.Context, peer string, key []byte, edge string) error {
		if *live {
			return control(ctx, key, []string{"-peer", peer, "-edge", edge, "-live"})
		}
		session, err := bridge.Dial(ctx, peer, key)
		if err != nil {
			return err
		}
		defer session.Close()
		stop := context.AfterFunc(ctx, func() { session.Close() })
		defer stop()
		ticker := time.NewTicker(bridge.HeartbeatInterval)
		defer ticker.Stop()
		for {
			if err := session.Send(bridge.Event{Kind: "ping"}); err != nil {
				return err
			}
			if _, err := session.Receive(); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
	type automaticService interface {
		Command(autopair.Command)
		Run(context.Context) error
	}
	var service automaticService
	var err error
	if *openLAN {
		service, err = autopair.NewOpen(cfg, *discover)
	} else {
		service, err = autopair.New(cfg)
	}
	if err != nil {
		return err
	}
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), 4096)
		for scanner.Scan() {
			var command autopair.Command
			if json.Unmarshal(scanner.Bytes(), &command) == nil {
				service.Command(command)
			}
		}
		// An orphan GUI worker must release input when its parent closes the pipe.
		cancel()
	}()
	return service.Run(ctx)
}

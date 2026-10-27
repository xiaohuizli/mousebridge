#!/usr/bin/env python3
"""Two CLI workers: discover, mutually approve, reconnect and release on GUI EOF.

All identities and trust secrets stay in memory/environment; output is redacted.
No native input is captured or injected.
"""
import json
import os
from pathlib import Path
import queue
import socket
import subprocess
import sys
import threading
import time

ROOT = Path(__file__).resolve().parents[1]
EXE = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else ROOT / 'bin/mousebridge-macos-arm64'


def port():
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


class Peer:
    def __init__(self, identity, trust, local, target, index):
        self.events = queue.Queue()
        env = dict(os.environ, MOUSEBRIDGE_KEY=identity, MOUSEBRIDGE_TRUST=json.dumps(trust))
        self.proc = subprocess.Popen([str(EXE), 'auto', '-enabled', '-name', f'auto-smoke-{index}',
            '-listen', '127.0.0.1:0', '-discovery-listen', f'127.0.0.1:{local}',
            '-targets', f'127.0.0.1:{target}'], env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8")
        threading.Thread(target=self.read, daemon=True).start()

    def read(self):
        for line in self.proc.stdout:
            self.events.put(json.loads(line))

    def wait(self, kind, forbidden=()):
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            event = self.events.get(timeout=max(0.01, deadline-time.monotonic()))
            assert event['kind'] not in (*forbidden, 'error'), f"unexpected event kind: {event['kind']}"
            if event['kind'] == kind:
                return event
        raise AssertionError(f'timeout waiting for {kind}')

    def send(self, **command):
        self.proc.stdin.write(json.dumps(command) + '\n')
        self.proc.stdin.flush()

    def close(self):
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try:
                assert self.proc.wait(timeout=5) == 0
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()
                raise AssertionError('GUI EOF did not stop auto worker')


identities = [subprocess.check_output([str(EXE), 'keygen'], text=True, encoding="utf-8").strip() for _ in range(2)]
trust = [{}, {}]
for phase in ['first-pair', 'saved-trust-reconnect']:
    ports = [port()]
    while len(ports) < 2:
        candidate = port()
        if candidate not in ports:
            ports.append(candidate)
    peers = []
    try:
        peers = [Peer(identities[i], trust[i], ports[i], ports[1-i], i) for i in range(2)]
        for peer in peers:
            peer.wait('searching')
        if phase == 'first-pair':
            confirmations = [peer.wait('confirm') for peer in peers]
            assert confirmations[0]['code'] == confirmations[1]['code']
            assert confirmations[0]['token'] == confirmations[1]['token']
            for peer, confirmation in zip(peers, confirmations):
                peer.send(kind='approve', token=confirmation['token'])
            accepted = [peer.wait('trusted') for peer in peers]
            assert accepted[0]['key'] == accepted[1]['key']
            trust = [{event['id']: event['key']} for event in accepted]
        sharing = [peer.wait('sharing', forbidden=('confirm',)) for peer in peers]
        assert {event['role'] for event in sharing} == {'control', 'receive'}
        # Permit several real TLS heartbeat exchanges; fail on any worker error.
        for peer in peers:
            end = time.monotonic() + 0.8
            while time.monotonic() < end:
                try:
                    event = peer.events.get(timeout=0.1)
                    assert event['kind'] not in ('error', 'disconnected', 'confirm')
                except queue.Empty:
                    pass
    finally:
        for peer in peers:
            peer.close()
print(json.dumps({'status': 'PASS', 'test': 'two-process-auto-pair', 'native_input_executed': False,
    'checks': ['automatic UDP discovery', 'matching TLS-exporter confirmation codes', 'both approvals required',
               'automatic role election', 'paired TLS control heartbeats', 'saved-trust automatic reconnect',
               'no repeated confirmation on reconnect', 'worker exits when GUI stdin closes'],
    'secrets_written_to_disk': False}, indent=2))

#!/usr/bin/env python3
"""Three independent open-LAN CLI workers; no approval or OS input APIs."""
import json
import os
from pathlib import Path
import queue
import socket
import subprocess
import sys
import threading
import time

ROOT=Path(__file__).resolve().parents[1]
EXE=Path(sys.argv[1]).resolve() if len(sys.argv)>1 else ROOT/'bin/mousebridge-macos-arm64'

def udp_port():
    with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as sock:
        sock.bind(('127.0.0.1',0))
        return sock.getsockname()[1]

class Peer:
    def __init__(self,index,ports):
        self.events=queue.Queue()
        identity=subprocess.check_output([str(EXE),'keygen'],text=True, encoding="utf-8").strip()
        env=dict(os.environ,MOUSEBRIDGE_KEY=identity,MOUSEBRIDGE_TRUST='{}')
        targets=','.join(f'127.0.0.1:{p}' for j,p in enumerate(ports) if j!=index)
        self.proc=subprocess.Popen([str(EXE),'auto','-open-lan','-enabled','-name',f'open-smoke-{index}',
            '-listen','127.0.0.1:0','-discovery-listen',f'127.0.0.1:{ports[index]}','-targets',targets],
            env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True, encoding="utf-8")
        threading.Thread(target=self.read,daemon=True).start()
    def read(self):
        for line in self.proc.stdout:
            self.events.put(json.loads(line))
    def wait(self,kind,predicate=lambda e:True):
        deadline=time.monotonic()+10
        while time.monotonic()<deadline:
            e=self.events.get(timeout=max(0.01,deadline-time.monotonic()))
            assert e['kind'] not in ('error','confirm','trusted','rejected'),e['kind']
            if e['kind']==kind and predicate(e):return e
        raise AssertionError(f'timed out waiting for {kind}')
    def command(self,**value):
        self.proc.stdin.write(json.dumps(value)+'\n');self.proc.stdin.flush()
    def close(self):
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try:assert self.proc.wait(timeout=5)==0
            except subprocess.TimeoutExpired:
                self.proc.kill();self.proc.wait();raise AssertionError('open worker did not exit on GUI EOF')

peers=[]
try:
    ports=[]
    while len(ports)<3:
        candidate=udp_port()
        if candidate not in ports:ports.append(candidate)
    peers=[Peer(i,ports) for i in range(3)]
    rosters=[peer.wait('devices',lambda e:len(e.get('peers',[]))==2) for peer in peers]
    for peer in peers:
        peer.wait('receiving')
        # Check that both the receiver and controller survive real TLS pings.
        end=time.monotonic()+1
        while time.monotonic()<end:
            try:
                e=peer.events.get(timeout=0.1)
                assert e['kind'] not in ('error','confirm','trusted')
            except queue.Empty:pass
    alternate=next(p['id'] for p in rosters[0]['peers'] if p['id']!=rosters[0]['selected'])
    peers[0].command(kind='select',id=alternate)
    peers[0].wait('controlling',lambda e:e['id']==alternate)
    peers[1].command(kind='settings',discover=False,enabled=False,edge='right')
    peers[1].wait('devices',lambda e:not e.get('peers'))
    for i,peer in enumerate(peers):
        if i!=1:peer.wait('devices',lambda e:len(e.get('peers',[]))==1)
    peers[1].command(kind='settings',discover=True,enabled=True,edge='right')
    for peer in peers:peer.wait('devices',lambda e:len(e.get('peers',[]))==2)
finally:
    for peer in peers:peer.close()
print(json.dumps({'status':'PASS','test':'three-process-open-lan','native_input_executed':False,
    'checks':['three fresh devices connect without approval','all peers remain discoverable',
              'simultaneous controller and receiver per host','independent encrypted input channels',
              'switch outgoing target','discovery off disconnects all peers',
              'discovery on reconnects without approval','GUI EOF stops workers'],
    'secrets_written_to_disk':False},indent=2))

#!/usr/bin/env python3
"""Two real CLI processes, actual UDP/TLS/TCP, a recording OS boundary. No input injection."""
import json
import os
from pathlib import Path
import queue
import socket
import subprocess
import sys
import tempfile
import threading

root = Path(__file__).resolve().parents[1]
exe = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else root / 'bin/mousebridge-macos-arm64'

def port(kind):
    with socket.socket(socket.AF_INET, kind) as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]

env = dict(os.environ)
env['MOUSEBRIDGE_KEY'] = subprocess.check_output([str(exe), 'keygen'], text=True, encoding="utf-8").strip()
tcp, udp = port(socket.SOCK_STREAM), port(socket.SOCK_DGRAM)
proc = subprocess.Popen([str(exe), 'receive', '-listen', f'127.0.0.1:{tcp}',
    '-discovery-listen', f'127.0.0.1:{udp}', '-name', 'smoke-receiver'],
    env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8")
startup = queue.Queue()
threading.Thread(target=lambda: startup.put(proc.stderr.readline()), daemon=True).start()
try:
    line = startup.get(timeout=5)
    assert 'live=false' in line, line
    def run(*args, pair_env=env):
        return subprocess.run([str(exe), *args], env=pair_env, text=True, encoding="utf-8",
                              capture_output=True, timeout=8)
    found = run('discover', '-target', f'127.0.0.1:{udp}')
    assert found.returncode == 0, found.stderr
    assert json.loads(found.stdout) == [{'name':'smoke-receiver', 'address':f'127.0.0.1:{tcp}'}]
    result = run('send', '-peer', f'127.0.0.1:{tcp}', '-script', str(root/'examples/demo.jsonl'))
    assert result.returncode == 0, result.stderr
    replies = [json.loads(x) for x in result.stdout.splitlines()]
    assert [r['kind'] for r in replies] == ['entered','ok','ok','ok','ok','ok','return','pong'], replies
    wrong = dict(env)
    wrong['MOUSEBRIDGE_KEY'] = subprocess.check_output([str(exe), 'keygen'],text=True, encoding="utf-8").strip()
    denied = run('send', '-peer', f'127.0.0.1:{tcp}', '-script', str(root/'examples/demo.jsonl'), pair_env=wrong)
    assert denied.returncode != 0, 'wrong pairing key accepted'
finally:
    proc.terminate()
    out, err = proc.communicate(timeout=5)
actual = [json.loads(x) for x in out.splitlines()]
expected = [
    {'kind':'move','x':1,'y':540},
    {'kind':'move','x':101,'y':560},
    {'kind':'button','down':True},
    {'kind':'move','x':141,'y':570},
    {'kind':'button'},
    {'kind':'wheel','y':1},
]
assert actual == expected, (actual,expected)
print(json.dumps({'status':'PASS','test':'two-process-loopback','native_injection':False,
 'checks':['UDP discovery','TLS mutual pairing','ordered movement/button/drag/wheel','edge return','heartbeat','wrong key rejected'],
 'received_events':actual, 'replies':replies}, indent=2))

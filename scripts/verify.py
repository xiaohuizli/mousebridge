#!/usr/bin/env python3
"""Rebuild and retain acceptance evidence, without capturing or injecting OS input."""
import datetime
import hashlib
import json
import platform
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[1]
out = root / 'docs/validation'
out.mkdir(parents=True, exist_ok=True)
commands = [
    ('tests', ['go','test','-race','-count=1','-json','./...']),
    ('vet', ['go','vet','./...']),
    ('vet-windows', ['env','GOOS=windows','GOARCH=amd64','CGO_ENABLED=0','go','vet','./...']),
    ('vet-windows-arm64', ['env','GOOS=windows','GOARCH=arm64','CGO_ENABLED=0','go','vet','./...']),
    ('build', ['make','build']),
    ('smoke', [sys.executable,'scripts/smoke.py','bin/mousebridge-macos-arm64']),
    ('smoke-auto', [sys.executable,'scripts/smoke_auto.py','bin/mousebridge-macos-arm64']),
    ('smoke-open', [sys.executable,'scripts/smoke_open.py','bin/mousebridge-macos-arm64']),
]
report = {'time_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
          'host':platform.platform(), 'go':subprocess.check_output(['go','version'],text=True).strip(),
          'native_input_executed':False, 'commands':[]}
for name, cmd in commands:
    run = subprocess.run(cmd, cwd=root, text=True, capture_output=True, timeout=180)
    (out/(name+'.log')).write_text(run.stdout+run.stderr)
    report['commands'].append({'name':name,'command':cmd,'exit_code':run.returncode})
    if name == 'smoke' and run.returncode == 0:
        (out/'smoke.json').write_text(run.stdout)
    if run.returncode:
        (out/'summary.json').write_text(json.dumps(report,indent=2))
        print(name, 'FAIL', run.stdout,run.stderr)
        sys.exit(1)
    print(name, 'PASS')
records=[json.loads(line) for line in (out/'tests.log').read_text().splitlines() if line.startswith('{')]
report['tests_passed']=[r['Test'] for r in records if r.get('Action')=='pass' and 'Test' in r]
report['binaries']={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted((root/'bin').iterdir()) if p.is_file()}
(out/'summary.json').write_text(json.dumps(report,indent=2)+'\n')

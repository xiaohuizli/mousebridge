#!/usr/bin/env python3
"""Create a portable OS/architecture-specific release ZIP; no OS input is run."""
import argparse
import hashlib
import json
import platform
from pathlib import Path
import re
import subprocess
import zipfile

root = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument('--platform', choices=['macos','windows'], required=True)
p.add_argument('--arch', choices=['amd64','arm64'], required=True)
p.add_argument('--version', default='0.1.0')
p.add_argument('--binary', type=Path, required=True)
p.add_argument('--output', type=Path, default=root/'dist')
a = p.parse_args()
if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?', a.version):
    p.error('version must be semver without a leading v')
a.binary = a.binary.resolve()
metadata = subprocess.check_output(['go','version','-m',str(a.binary)], text=True)
expected_os = 'darwin' if a.platform == 'macos' else 'windows'
for item in (f'GOOS={expected_os}',f'GOARCH={a.arch}'):
    if not re.search(r'(?m)^\s*build\s+'+re.escape(item)+r'\s*$',metadata):
        p.error(f'binary metadata does not match {item}')
if expected_os == 'darwin' and 'CGO_ENABLED=1' not in metadata:
    p.error('macOS package requires native cgo input support')
name = f'mousebridge-{a.platform}-{a.arch}'
binary_name = name + ('.exe' if a.platform == 'windows' else '')
files = {f'bin/{binary_name}':a.binary}
for rel in ['README.md','docs/BEHAVIOR.md','docs/PROTOCOL.md','docs/VALIDATION.md','docs/RELEASE_NOTES.md','examples/demo.jsonl']:
    files[rel] = root/rel
for file in sorted((root/'docs/validation').glob('*')):
    if file.is_file(): files[file.relative_to(root).as_posix()] = file
info = {'version':a.version,'platform':a.platform,'architecture':a.arch,
        'commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),
        'builder':platform.platform(),'go_metadata':metadata,
        'signed':False,'notarized':False,'native_input_verified':False,
        'binary_sha256':hashlib.sha256(a.binary.read_bytes()).hexdigest()}
contents = {rel:path.read_bytes() for rel,path in files.items()}
contents['BUILD-INFO.json'] = (json.dumps(info,indent=2)+'\n').encode()
manifest = ''.join(f'{hashlib.sha256(data).hexdigest()}  {rel}\n' for rel,data in sorted(contents.items()))
contents['SHA256SUMS'] = manifest.encode()
a.output.mkdir(parents=True,exist_ok=True)
archive = a.output/f'mousebridge-{a.version}-{a.platform}-{a.arch}.zip'
with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED) as z:
    for rel,data in sorted(contents.items()):
        entry = zipfile.ZipInfo(f'{name}/{rel}',date_time=(2026,1,1,0,0,0))
        entry.create_system = 3
        entry.external_attr = (0o100755 if rel.startswith('bin/') else 0o100644)<<16
        entry.compress_type=zipfile.ZIP_DEFLATED
        z.writestr(entry,data)
with zipfile.ZipFile(archive) as z:
    if z.testzip() is not None: raise RuntimeError('ZIP integrity failure')
    for rel,data in contents.items():
        assert z.read(f'{name}/{rel}') == data, rel
checksum=hashlib.sha256(archive.read_bytes()).hexdigest()
archive.with_suffix('.zip.sha256').write_text(f'{checksum}  {archive.name}\n')
print(f'{archive.name}: {checksum}')

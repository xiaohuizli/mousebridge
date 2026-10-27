#!/usr/bin/env python3
"""Cross-build native Windows GUIs/cores and a per-user dual-architecture installer."""
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import struct
import subprocess
import zipfile

ROOT=Path(__file__).resolve().parents[1]

def run(*command,env=None):
    subprocess.run([str(x) for x in command],cwd=ROOT,env=env,check=True)

def pe_info(path):
    data=path.read_bytes()
    assert data[:2]==b'MZ'
    offset=struct.unpack_from('<I',data,0x3c)[0]
    assert data[offset:offset+4]==b'PE\0\0'
    return {'machine':struct.unpack_from('<H',data,offset+4)[0],
            'subsystem':struct.unpack_from('<H',data,offset+24+68)[0]}

def main():
    version=plistlib.loads((ROOT/'macos/Info.plist').read_bytes())['CFBundleShortVersionString']
    compiler=shutil.which('makensis')
    if not compiler and os.name=='nt':
        candidate=Path(os.environ.get('ProgramFiles(x86)',os.environ.get('ProgramFiles','')))/'NSIS/makensis.exe'
        if candidate.is_file():compiler=str(candidate)
    if not compiler:raise SystemExit('makensis is required to build the Windows installer')
    build=ROOT/'build/windows-app';output=ROOT/'dist/windows-app'
    build.mkdir(parents=True,exist_ok=True);output.mkdir(parents=True,exist_ok=True)
    # The installed copy has a plain-text help file readable by Notepad.
    (build/'WINDOWS_APP.txt').write_text((ROOT/'resources/windows/HELP.md').read_text(encoding="utf-8"),encoding='utf-8-sig')
    commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True, encoding="utf-8").strip()
    for arch,machine in [('amd64',0x8664),('arm64',0xaa64)]:
        directory=build/arch;directory.mkdir(exist_ok=True)
        env=dict(os.environ,GOOS='windows',GOARCH=arch,CGO_ENABLED='0')
        run('go','build','-trimpath','-ldflags=-H=windowsgui','-o',directory/'MouseBridge.exe','./cmd/mousebridge-desktop',env=env)
        run('go','build','-trimpath','-o',directory/'mousebridge-core.exe','./cmd/mousebridge',env=env)
        assert pe_info(directory/'MouseBridge.exe')=={'machine':machine,'subsystem':2}
        assert pe_info(directory/'mousebridge-core.exe')=={'machine':machine,'subsystem':3}
        metadata={'version':version,'platform':'windows','architecture':arch,'source_commit':commit,
            'source_dirty':bool(subprocess.check_output(['git','status','--porcelain'],cwd=ROOT,text=True, encoding="utf-8").strip()),
            'authenticode_signed':False,'native_input_verified':False,'windows_runtime_verified':False,
            'identity_storage':'process memory only',
            'source_sha256':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest()
                for d in ['cmd','internal','windows','scripts','resources'] for p in sorted((ROOT/d).rglob('*'))
                if p.suffix in {'.go','.c','.h','.nsi','.py','.md'}},
            'binaries':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in directory.glob('*.exe')}}
        (directory/'BUILD-INFO.json').write_text(json.dumps(metadata,indent=2)+'\n')
        files={p.name:p.read_bytes() for p in directory.iterdir() if p.name in ['MouseBridge.exe','mousebridge-core.exe','BUILD-INFO.json']}
        files['使用说明.txt']=(build/'WINDOWS_APP.txt').read_bytes()
        sums=''.join(f'{hashlib.sha256(data).hexdigest()}  {name}\n' for name,data in sorted(files.items()))
        (directory/'SHA256SUMS').write_text(sums,encoding='utf-8')
        files['SHA256SUMS']=sums.encode()
        archive=output/f'MouseBridge-{version}-windows-{arch}-portable.zip'
        with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as z:
            for name,data in files.items():z.writestr(f'MouseBridge/{name}',data)
        with zipfile.ZipFile(archive) as z:assert z.testzip() is None
        archive.with_suffix('.zip.sha256').write_text(f'{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n')
    installer=output/f'MouseBridge-{version}-windows-setup.exe'
    switch='/' if os.name=='nt' else '-'
    run(compiler,switch+'WX',switch+'V3',switch+'INPUTCHARSET','UTF8',f'{switch}DVERSION={version}',f'{switch}DPAYLOAD={build}',f'{switch}DOUTPUT={installer}',ROOT/'windows/installer.nsi')
    assert pe_info(installer)['machine']==0x14c  # NSIS bootstrap; installs the native payload for the OS.
    installer.with_suffix('.exe.sha256').write_text(f'{hashlib.sha256(installer.read_bytes()).hexdigest()}  {installer.name}\n')
    print(f'Installer: {installer}',flush=True)
    print('Native Windows amd64 + arm64 GUI/core PE headers and portable ZIPs verified.',flush=True)

if __name__=='__main__':main()

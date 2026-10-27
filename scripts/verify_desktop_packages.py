#!/usr/bin/env python3
"""Verify desktop distributions without capturing/injecting system input.

On Windows also silently install/uninstall into an isolated build directory.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import struct
import subprocess
import tempfile
import time
import zipfile

ROOT=Path(__file__).resolve().parents[1]
VERSION=plistlib.loads((ROOT/'macos/Info.plist').read_bytes())['CFBundleShortVersionString']

def run(*args,timeout=120):
    return subprocess.run([str(x) for x in args],cwd=ROOT,capture_output=True,text=True, encoding="utf-8",check=True,timeout=timeout)

def checksum(path):
    value=hashlib.sha256(path.read_bytes()).hexdigest()
    assert path.with_suffix(path.suffix+'.sha256').read_text(encoding="utf-8").split()[0]==value,path
    return value

def sources(metadata):
    assert metadata['version']==VERSION
    for path,digest in metadata['source_sha256'].items():
        assert hashlib.sha256((ROOT/path).read_bytes()).hexdigest()==digest,path

def pe(data):
    assert data[:2]==b'MZ'
    offset=struct.unpack_from('<I',data,60)[0]
    assert data[offset:offset+4]==b'PE\0\0'
    return struct.unpack_from('<H',data,offset+4)[0],struct.unpack_from('<H',data,offset+92)[0]

def verify_macos(report):
    directory=ROOT/'dist/macos-app'
    dmg=directory/f'MouseBridge-{VERSION}-macos-universal.dmg'
    archive=directory/f'MouseBridge-{VERSION}-macos-universal.zip'
    for path in [dmg,archive]:report['artifacts'][path.name]=checksum(path)
    with zipfile.ZipFile(archive) as z:assert z.testzip() is None
    with tempfile.TemporaryDirectory(prefix='mousebridge-zip-') as temp:
        run('ditto','-x','-k',archive,temp)
        app=Path(temp)/'MouseBridge.app'
        run('codesign','--verify','--deep','--strict',app)
        metadata=json.loads((app/'Contents/Resources/BUILD-INFO.json').read_text(encoding="utf-8"))
        sources(metadata);assert metadata['keychain_access'] is False
        binary=app/'Contents/MacOS/MouseBridge'
        symbols=run('xcrun','nm','-u',binary).stdout
        assert 'SecItem' not in symbols and 'SecKeychain' not in symbols
        for path in [binary,app/'Contents/Resources/mousebridge']:
            assert set(run('xcrun','lipo','-archs',path).stdout.split())=={'arm64','x86_64'}
    run('hdiutil','verify',dmg)
    mounted=plistlib.loads(subprocess.check_output(['hdiutil','attach','-readonly','-nobrowse','-plist',str(dmg)]))
    volume=next(Path(item['mount-point']) for item in mounted['system-entities'] if 'mount-point' in item)
    try:
        app=volume/'MouseBridge.app';run('codesign','--verify','--deep','--strict',app)
        assert (volume/'Applications').is_symlink()
        core=app/'Contents/Resources/mousebridge'
        assert run(core,'version').stdout.strip()=='MouseBridge '+VERSION
        report['checks']['mounted_dmg_open_lan']=json.loads(run('python3','scripts/smoke_open.py',core).stdout)
    finally:run('hdiutil','detach',volume)
    report['checks']['universal_signatures_sources_no_keychain_zip_dmg']=True

def verify_windows(report):
    directory=ROOT/'dist/windows-app'
    installer=directory/f'MouseBridge-{VERSION}-windows-setup.exe'
    report['artifacts'][installer.name]=checksum(installer)
    assert pe(installer.read_bytes())[0]==0x14c
    for arch,machine in [('amd64',0x8664),('arm64',0xaa64)]:
        archive=directory/f'MouseBridge-{VERSION}-windows-{arch}-portable.zip'
        report['artifacts'][archive.name]=checksum(archive)
        with zipfile.ZipFile(archive) as z:
            assert z.testzip() is None
            sources(json.loads(z.read('MouseBridge/BUILD-INFO.json')))
            assert pe(z.read('MouseBridge/MouseBridge.exe'))==(machine,2)
            assert pe(z.read('MouseBridge/mousebridge-core.exe'))==(machine,3)
            for line in z.read('MouseBridge/SHA256SUMS').decode().splitlines():
                digest,path=line.split('  ',1)
                assert hashlib.sha256(z.read('MouseBridge/'+path)).hexdigest()==digest
    report['checks']['pe_headers_sources_checksums_and_portable_zips']=True
    if os.name!='nt' or os.environ.get('GITHUB_ACTIONS')!='true':
        report['checks']['install_uninstall']='not run: isolated Windows CI only'
        return
    destination=ROOT/'build/installer-check'
    assert not destination.exists(),'installer test directory must be fresh'
    # NSIS /D consumes the rest of the command line; keep it last. CI's checkout
    # path has no spaces, avoiding ambiguity in older NSIS argument parsing.
    assert not any(c.isspace() for c in str(destination)), 'use a CI checkout path without spaces'
    try:
        run(installer,'/S','/D='+str(destination))
        assert (destination/'Uninstall.exe').is_file()
        for binary in ['MouseBridge.exe','mousebridge-core.exe']:
            assert (destination/binary).is_file()
        assert run(destination/'mousebridge-core.exe','version').stdout.strip()=='MouseBridge '+VERSION
        import platform
        native='arm64' if platform.machine().lower() in ['arm64','aarch64'] else 'amd64'
        payload=ROOT/'build/windows-app'/native
        for name in ['MouseBridge.exe','mousebridge-core.exe']:
            assert (destination/name).read_bytes()==(payload/name).read_bytes()
        report['checks']['installed_core_open_lan']=json.loads(run('python','scripts/smoke_open.py',destination/'mousebridge-core.exe').stdout)
    finally:
        uninstaller=destination/'Uninstall.exe'
        if uninstaller.exists():
            run(uninstaller,'/S')
            deadline=time.monotonic()+30
            while any((destination/name).exists() for name in ['MouseBridge.exe','mousebridge-core.exe','Uninstall.exe']) and time.monotonic()<deadline:
                time.sleep(0.2)
    assert not (destination/'MouseBridge.exe').exists()
    assert not (destination/'mousebridge-core.exe').exists()
    report['checks']['silent_install_native_payload_core_and_uninstall']=True

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--platform',choices=['macos','windows'],required=True)
    args=parser.parse_args()
    report={'version':VERSION,'platform':args.platform,'native_input_executed':False,'checks':{},'artifacts':{}}
    if args.platform=='macos':verify_macos(report)
    else:verify_windows(report)
    output=ROOT/f'dist/{"macos-app" if args.platform=="macos" else "windows-app"}/verification.json'
    output.write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))

if __name__=='__main__':main()

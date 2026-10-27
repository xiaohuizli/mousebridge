#!/usr/bin/env python3
"""Build a native, universal macOS launcher and its bundled Go worker.

No Developer ID or notarization credentials are required for a local ad-hoc build.
The app never captures/injects input as part of packaging.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def run(*args, env=None):
    subprocess.run([str(arg) for arg in args], cwd=ROOT, env=env, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=ROOT / 'dist/macos-app')
    args = parser.parse_args()
    info = plistlib.loads((ROOT / 'macos/Info.plist').read_bytes())
    version = info['CFBundleShortVersionString']
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    build = ROOT / 'build/macos-app'
    build.mkdir(parents=True, exist_ok=True)
    app = build / 'MouseBridge.app'
    if app.exists():
        shutil.rmtree(app)
    executables = app / 'Contents/MacOS'
    resources = app / 'Contents/Resources'
    executables.mkdir(parents=True)
    resources.mkdir(parents=True)
    shutil.copy2(ROOT / 'macos/Info.plist', app / 'Contents/Info.plist')
    for arch in ('arm64', 'amd64'):
        swift_arch = 'x86_64' if arch == 'amd64' else arch
        run('xcrun', 'swiftc', '-swift-version', '5', '-O', '-target', f'{swift_arch}-apple-macosx12.0',
            '-framework', 'AppKit', '-framework', 'ApplicationServices', '-framework', 'Security',
            'macos/MouseBridge.swift', '-o', build / f'launcher-{arch}')
        env = dict(os.environ, GOOS='darwin', GOARCH=arch, CGO_ENABLED='1', MACOSX_DEPLOYMENT_TARGET='12.0',
                   CGO_CFLAGS='-O2 -g -mmacosx-version-min=12.0',
                   CGO_LDFLAGS='-O2 -g -mmacosx-version-min=12.0')
        # Go's macOS runtime currently requires macOS 12 or newer.
        run('go', 'build', '-trimpath', '-o', build / f'worker-{arch}', './cmd/mousebridge', env=env)
    run('xcrun', 'lipo', '-create', build / 'launcher-arm64', build / 'launcher-amd64',
        '-output', executables / 'MouseBridge')
    run('xcrun', 'lipo', '-create', build / 'worker-arm64', build / 'worker-amd64',
        '-output', resources / 'mousebridge')
    run('xcrun', 'swift', 'macos/Icon.swift', build / 'MouseBridge.iconset')
    run('xcrun', 'iconutil', '-c', 'icns', build / 'MouseBridge.iconset', '-o', resources / 'MouseBridge.icns')
    shutil.copy2(ROOT / 'resources/macos/HELP.md', resources / '使用说明.md')
    shutil.copy2(ROOT / 'examples/demo.jsonl', resources / 'demo.jsonl')
    metadata = {
        'version': version, 'architectures': ['arm64', 'x86_64'], 'minimum_macos': '12.0',
        'source_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'source_dirty': bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip()),
        'signature': 'ad-hoc (local build)', 'notarized': False, 'native_input_verified': False,
        'identity_storage': 'process memory only', 'keychain_access': False,
        'source_sha256': {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest()
                          for path in sorted([ROOT / 'go.mod'] + [path for directory in ['cmd', 'internal', 'macos', 'scripts', 'resources']
                              for path in (ROOT / directory).rglob('*') if path.suffix in {'.go', '.c', '.h', '.swift', '.plist', '.py', '.md'}])},
    }
    (resources / 'BUILD-INFO.json').write_text(json.dumps(metadata, indent=2) + '\n')
    run('plutil', '-lint', app / 'Contents/Info.plist')
    run('codesign', '--force', '--sign', '-', '--identifier', 'io.mousebridge.worker', resources / 'mousebridge')
    run('codesign', '--force', '--sign', '-', '--identifier', 'io.mousebridge.desktop', app)
    run('codesign', '--verify', '--deep', '--strict', '--verbose=2', app)
    info = plistlib.loads((app / 'Contents/Info.plist').read_bytes())
    assert info['CFBundleExecutable'] == 'MouseBridge'
    destination = output / 'MouseBridge.app'
    if destination.exists():
        shutil.rmtree(destination)
    run('ditto', '--norsrc', '--noextattr', app, destination)
    run('codesign', '--verify', '--deep', '--strict', destination)
    archive = output / f'MouseBridge-{version}-macos-universal.zip'
    run('ditto', '-c', '-k', '--sequesterRsrc', '--keepParent', destination, archive)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    archive.with_suffix('.zip.sha256').write_text(f'{digest}  {archive.name}\n')
    stage = build / 'dmg-stage'
    if stage.exists():
        shutil.rmtree(stage)
    stage.mkdir()
    run('ditto', '--norsrc', '--noextattr', destination, stage / 'MouseBridge.app')
    (stage / 'Applications').symlink_to('/Applications')
    dmg = output / f'MouseBridge-{version}-macos-universal.dmg'
    run('hdiutil', 'create', '-ov', '-format', 'UDZO', '-fs', 'HFS+',
        '-volname', f'MouseBridge {version}', '-srcfolder', stage, dmg)
    dmg.with_suffix('.dmg.sha256').write_text(f'{hashlib.sha256(dmg.read_bytes()).hexdigest()}  {dmg.name}\n')
    print(f'App: {destination}\nZIP: {archive}\nSHA256: {digest}', flush=True)
    print(f'DMG: {dmg}', flush=True)


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Require identical GUI/core/package versions, including release tag when set."""
import os
from pathlib import Path
import plistlib
import re

ROOT=Path(__file__).resolve().parents[1]
version=plistlib.loads((ROOT/'macos/Info.plist').read_bytes())['CFBundleShortVersionString']
for name in ['cmd/mousebridge/main.go','cmd/mousebridge-desktop/main_windows.go']:
    match=re.search(r'const version\s*=\s*"([^"]+)"',(ROOT/name).read_text(encoding="utf-8"))
    if match is None or match[1]!=version:raise SystemExit(f'version mismatch: {name}')
if os.environ.get('GITHUB_REF_TYPE')=='tag' and os.environ.get('GITHUB_REF_NAME')!='v'+version:
    raise SystemExit(f'release tag must match v{version}')
print(version)

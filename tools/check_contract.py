#!/usr/bin/env python3
"""Regenerate OpenAPI and TypeScript, fail on drift (works without Git metadata)."""
import subprocess,sys
from pathlib import Path
root=Path(__file__).resolve().parents[1]
paths=[root/"api/openapi.json",root/"web/src/generated/api.ts"]
before=[p.read_bytes() for p in paths]
subprocess.run([sys.executable,"tools/generate_openapi.py"],cwd=root,check=True)
npm="npm.cmd" if sys.platform=="win32" else "npm"
subprocess.run([npm,"exec","--","openapi-typescript","../api/openapi.json","-o","src/generated/api.ts"],cwd=root/"web",check=True)
different=[str(p.relative_to(root)) for p,b in zip(paths,before) if p.read_bytes()!=b]
if different:raise SystemExit("Contract drift detected (regenerated files retained): "+", ".join(different))
print("OpenAPI and generated TypeScript match their generator.")


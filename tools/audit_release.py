#!/usr/bin/env python3
"""Verify actual release hashes, versions, allowed contents and secret boundaries."""
import argparse,hashlib,json,re,zipfile
from pathlib import Path
from release_version import VERSION
p=argparse.ArgumentParser();p.add_argument("--release-dir",type=Path,required=True);a=p.parse_args()
root=a.release_dir;checks=[]
sha=lambda b:hashlib.sha256(b).hexdigest()
sumfile=root/f"FlowDesk_{VERSION}_SHA256SUMS.txt"
for line in sumfile.read_text().splitlines():
 expected,name=line.split("  ",1);assert sha((root/name).read_bytes())==expected,"archive checksum mismatch"
checks.append("archive SHA-256")
for package in sorted(root.glob(f"FlowDesk_{VERSION}_*.zip")):
 if package.name.endswith("_evidence.zip"):
  with zipfile.ZipFile(package) as z:
   assert z.testzip() is None
   entries=z.namelist();prefix=package.stem+"/"
   manifest=json.loads(z.read(prefix+"EVIDENCE_MANIFEST.json"))
   assert manifest["software_version"]==VERSION
   assert set(entries)=={prefix+n for n in manifest["files"]}|{prefix+"EVIDENCE_MANIFEST.json"}
   for path,digest in manifest["files"].items():
    assert not path.startswith("/") and ".." not in Path(path).parts
    assert sha(z.read(prefix+path))==digest
    raw=z.read(prefix+path)
    assert (b"/workspace/"+b"scratch/") not in raw and (b"ai-"+b"ticket-work") not in raw
   evidence_meta=json.loads(z.read(prefix+"BUILD_METADATA.json"))
   assert evidence_meta["source_input_sha256"]==manifest["source_input_sha256"]
  continue
 with zipfile.ZipFile(package) as z:
  entries=z.namelist();assert all(not n.startswith("/") and ".." not in Path(n).parts for n in entries)
  assert not any(any(p in ("node_modules","data",".git","persistence-helpers-implementation") for p in Path(n).parts) for n in entries)
  assert not any("INTERNAL_DESIGN" in n or "IMPLEMENTATION_STATUS" in n or "PENDING_FEEDBACK" in n or n.endswith((".db",".key",".log",".db-wal",".db-shm")) for n in entries)
  prefix=entries[0].split("/")[0]+"/"
  metadata=json.loads(z.read(prefix+"BUILD_METADATA.json"));manifest=json.loads(z.read(prefix+"SOURCE_MANIFEST.json"));assert metadata["software_version"]==manifest["software_version"]==VERSION
  assert metadata["source_input_sha256"]==manifest["source_input_sha256"]
  assert sha(json.dumps(manifest["files"],sort_keys=True,separators=(",",":")).encode())==manifest["source_input_sha256"]
  is_source=package.name.endswith("_source.zip")
  if is_source:
   for path,digest in manifest["files"].items():assert sha(z.read(prefix+path))==digest,path
   pkg=json.loads(z.read(prefix+"web/package.json"));assert pkg["version"]==VERSION
   contract=json.loads(z.read(prefix+"api/openapi.json"));assert contract["info"]["version"]==VERSION
   for path,digest in metadata["frontend_assets_sha256"].items():assert sha(z.read(prefix+"web/dist/"+path))==digest,path
   for n in entries:
    if not n.endswith((".go",".py",".cjs",".ts",".tsx",".json",".md",".yml",".html",".css",".txt")):continue
    raw=z.read(n)
    assert (b"/workspace/"+b"scratch/") not in raw and (b"ai-"+b"ticket-work") not in raw,"private workspace path"
    assert not re.search(rb"sk-[A-Za-z0-9_-]{24,}",raw),"provider credential-shaped content"
    assert not re.search(rb"(ghp_|github_pat_)[A-Za-z0-9_]{20,}",raw),"Git token-shaped content"
    if n.endswith((".go",".ts",".tsx",".py")):assert b"\x00" not in raw,"NUL in source"
  else:
   executable="flowdesk.exe" if "windows" in package.name else "flowdesk"
   assert sha(z.read(prefix+executable))==metadata["binaries"][executable]["sha256"]
   assert prefix+"README.txt" in entries and prefix+"LICENSE" in entries and prefix+"licenses/INVENTORY.json" in entries
   assert not any(n.endswith((".go",".ts",".tsx",".db",".key")) for n in entries),"unexpected runtime-package source or secret"
   if "windows" in package.name:
    assert prefix+"故障诊断.cmd" in entries,"missing Windows diagnostic entry"
    for cmdfile in ("启动FlowDesk.cmd","故障诊断.cmd","启动个人工作台.cmd","个人工作台故障诊断.cmd"):
     raw=z.read(prefix+cmdfile);assert b"\r\n" in raw and b"\n" not in raw.replace(b"\r\n",b""),"CMD line ending mismatch"
   for doc in entries:
    if not doc.endswith(".md"):continue
    for link in re.findall(r"\]\(([^)]+)\)",z.read(doc).decode()):
     if "://" in link or link.startswith("#"):continue
     assert (Path(doc).parent/link.split("#")[0]).as_posix() in entries,"runtime documentation link missing"
checks.extend(["source input manifest","matching versions/binaries/assets","public allowlist","no workspace/secret/private design/path","source text encoding"])
print(json.dumps({"status":"passed","checks":checks},indent=2))


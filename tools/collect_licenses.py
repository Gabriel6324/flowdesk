#!/usr/bin/env python3
"""Snapshot license files from the actual module cache and installed npm tree."""
import argparse,hashlib,json,re,shutil,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument("--go",default="go");a=p.parse_args()
root=Path(__file__).resolve().parents[1];dest=root/"licenses";dest.mkdir(exist_ok=True)
raw=subprocess.check_output([a.go,"list","-m","-json","all"],cwd=root).decode()
decoder=json.JSONDecoder();mods=[]
while raw.strip():
 raw=raw.lstrip();obj,idx=decoder.raw_decode(raw);raw=raw[idx:]
 if not obj.get("Main"):mods.append(obj)
def slug(s):return re.sub(r"[^A-Za-z0-9_.-]+","_",s)
def copied(group,identity,base):
 found=[]
 # Only the root and one nested directory; dependency subtrees have their own entries.
 for f in sorted(base.iterdir()):
  candidates=[f] if f.is_file() else list(f.iterdir()) if f.is_dir() else []
  for f in candidates:
   if not f.is_file():continue
   n=f.name.lower()
   if not n.startswith(("license","licence","copying","notice")) or f.stat().st_size>2_000_000:continue
   rel=Path(group)/slug(identity)/f.relative_to(base);target=dest/rel
   target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(f,target)
   found.append({"path":"licenses/"+rel.as_posix(),"sha256":hashlib.sha256(target.read_bytes()).hexdigest()})
 return found
inventory=[];missing=[]
for m in mods:
 base=Path(m["Dir"]);files=copied("go",m["Path"]+"@"+m["Version"],base)
 if not files:missing.append(m["Path"])
 inventory.append({"ecosystem":"go","name":m["Path"],"version":m["Version"],"scope":"binary/source dependency","license_files":files})
goroot=Path(subprocess.check_output([a.go,"env","GOROOT"]).decode().strip())
target=dest/"go"/"toolchain"/"LICENSE";target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(goroot/"LICENSE",target)
inventory.append({"ecosystem":"go","name":"Go standard library/runtime","version":subprocess.check_output([a.go,"version"]).decode().strip(),"scope":"binary","license_files":[{"path":"licenses/go/toolchain/LICENSE","sha256":hashlib.sha256(target.read_bytes()).hexdigest()}]})
lock=json.loads((root/"web/package-lock.json").read_text())
for location,meta in sorted(lock["packages"].items()):
 if not location or "node_modules/" not in location:continue
 base=root/"web"/location
 if not (base/"package.json").is_file():
  if meta.get("optional"):continue
  missing.append(location);continue
 pkg=json.loads((base/"package.json").read_text());identity=pkg["name"]+"@"+pkg["version"];files=copied("npm",identity,base)
 if not files:
  target=dest/"npm"/slug(identity)/"PACKAGE_METADATA.json";target.parent.mkdir(parents=True,exist_ok=True);target.write_text(json.dumps({k:pkg[k] for k in ("name","version","license","author","contributors","repository") if k in pkg},indent=2)+"\n")
  files=[{"path":target.relative_to(root).as_posix(),"sha256":hashlib.sha256(target.read_bytes()).hexdigest(),"note":"package metadata only; inspect upstream license before changing distribution scope"}]
 inventory.append({"ecosystem":"npm","name":pkg["name"],"version":pkg["version"],"declared_license":pkg.get("license"),"scope":"build/test" if meta.get("dev") else "frontend dependency","license_files":files})
(root/"licenses/INVENTORY.json").write_text(json.dumps({"packages":inventory,"missing_go_or_required_install":missing,"scope":"actual installed tree; optional packages absent on this platform are omitted"},ensure_ascii=False,indent=2)+"\n")
lines=["# Third-party notices","","License files are copied unchanged from the installed dependency versions. See licenses/INVENTORY.json for versions, scope, paths and SHA-256. FlowDesk's MIT license does not replace third-party terms.","","Go standard library/runtime uses its included BSD-style license. The SQLite distribution includes separate SQLite and third-party notices; review all supplied files. Build/test dependencies are recorded separately from frontend dependencies.","","| Ecosystem | Package | Version | Scope | License/notice files |","| --- | --- | --- | --- | --- |"]
for x in inventory:
 lines.append(f"| {x['ecosystem']} | {x['name']} | {x['version']} | {x['scope']} | "+", ".join(f"[{Path(y['path']).name}]({y['path']})" for y in x["license_files"])+" |")
(root/"THIRD_PARTY_NOTICES.md").write_text("\n".join(lines)+"\n")
if missing:raise SystemExit("Missing dependency license/install: "+", ".join(missing))
print(f"Recorded {len(inventory)} installed dependencies/runtime entries.")


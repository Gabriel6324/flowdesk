#!/usr/bin/env python3
"""Archive only public verification records matched to the current build."""
import argparse, hashlib, json, re, zipfile
from pathlib import Path
from release_version import ROOT, VERSION

p = argparse.ArgumentParser()
p.add_argument("--release-dir", type=Path, required=True)
p.add_argument("--performance-full", type=Path, required=True)
p.add_argument("--package-audit", type=Path, required=True)
a = p.parse_args()
meta = json.loads((ROOT / "build/BUILD_METADATA.json").read_text())
assert meta["software_version"] == VERSION
performance = json.loads(a.performance_full.read_text())
assert performance["source_hash"] == meta["source_input_sha256"] and performance["software_version"] == VERSION
audit = json.loads(a.package_audit.read_text())
assert audit["status"] == "passed"
files = {}
for f in sorted((ROOT / "docs/evidence").iterdir()):
    if f.is_file() and f.suffix in (".json", ".jsonl", ".md", ".csv", ".txt"):
        files["docs/evidence/" + f.name] = f.read_bytes()
for name in ("VERIFICATION.md", "PERFORMANCE.md", "CHANGELOG.md"):
    files["docs/" + name] = (ROOT / "docs" / name).read_bytes()
for name in ("BUILD_METADATA.json", "SOURCE_MANIFEST.json"):
    files[name] = (ROOT / "build" / name).read_bytes()
files["docs/evidence/performance-full.json"] = a.performance_full.read_bytes()
files["docs/evidence/package-audit.json"] = a.package_audit.read_bytes()
files["README.md"] = (
    f"# FlowDesk {VERSION} 验证记录\n\n"
    "docs/VERIFICATION.md 说明已执行检查和未验证项；docs/PERFORMANCE.md 说明固定负载的条件及限制。\n"
    "docs/evidence 包含 Go race、前端组件、实际进程、模拟协议、评分模板和完整逐请求性能记录。\n"
    "模拟协议成功不是实际模型效果，未评审的语义评分不得视为通过；本机封闭负载不等于生产容量。\n"
    "已执行的浏览器检查见 browser-smoke.json；Windows原生、Docker/TLS和真实模型仍需独立验收；没有付费模型调用或公网部署。\n"
    f"\nSource input SHA-256: {meta['source_input_sha256']}\n"
).encode()
for name, raw in files.items():
    assert ".." not in Path(name).parts and not name.startswith("/")
    assert (b"/workspace/" + b"scratch/") not in raw and (b"ai-" + b"ticket-work") not in raw
    assert not re.search(rb"sk-[A-Za-z0-9_-]{24,}|(?:ghp_|github_pat_)[A-Za-z0-9_]{20,}", raw)
    if name.endswith(".json"):
        json.loads(raw)
manifest = {"software_version": VERSION, "source_input_sha256": meta["source_input_sha256"], "files": {name: hashlib.sha256(raw).hexdigest() for name, raw in sorted(files.items())}}
files["EVIDENCE_MANIFEST.json"] = (json.dumps(manifest, ensure_ascii=False, indent=2) + "\n").encode()
a.release_dir.mkdir(parents=True, exist_ok=True)
archive = a.release_dir / f"FlowDesk_{VERSION}_evidence.zip"
prefix = archive.stem + "/"
with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for name, raw in sorted(files.items()):
        info = zipfile.ZipInfo(prefix + name)
        info.external_attr = 0o100644 << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        z.writestr(info, raw, compresslevel=9)
with zipfile.ZipFile(archive) as z:
    assert z.testzip() is None
    for name, digest in manifest["files"].items():
        assert hashlib.sha256(z.read(prefix + name)).hexdigest() == digest
checksums = [hashlib.sha256(f.read_bytes()).hexdigest() + "  " + f.name for f in sorted(a.release_dir.glob(f"FlowDesk_{VERSION}_*.zip"))]
(a.release_dir / f"FlowDesk_{VERSION}_SHA256SUMS.txt").write_text("\n".join(checksums) + "\n")
print(f"Evidence archive: {len(files)} files; explicit allowlist, hashes and ZIP integrity passed.")

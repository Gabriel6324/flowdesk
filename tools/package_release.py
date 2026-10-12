#!/usr/bin/env python3
"""Build and package explicit public inputs. Never infer deliverables by zipping the workspace."""
import argparse,datetime,hashlib,json,os,shutil,subprocess,zipfile
from pathlib import Path
from release_version import VERSION
ROOT=Path(__file__).resolve().parents[1];BUILD=ROOT/"build"
p=argparse.ArgumentParser();p.add_argument("--go",default="go");p.add_argument("--out",type=Path,default=BUILD/"release")
p.add_argument("--freeze-only",action="store_true");p.add_argument("--build-only",action="store_true");p.add_argument("--package-only",action="store_true")
a=p.parse_args()
if sum((a.freeze_only,a.build_only,a.package_only))>1:raise SystemExit("Choose at most one phase.")
BUILD.mkdir(exist_ok=True)
for name in ("web/package.json","web/package-lock.json"):
 if json.loads((ROOT/name).read_text())["version"]!=VERSION:raise SystemExit("Frontend/backend version mismatch: "+name)
if json.loads((ROOT/"api/openapi.json").read_text())["info"]["version"]!=VERSION:raise SystemExit("API/backend version mismatch. Regenerate the contract.")
def sha(b):return hashlib.sha256(b).hexdigest()
def inputs():
 direct=["go.mod","go.sum",".gitignore",".gitattributes",".dockerignore","LICENSE","README.md","THIRD_PARTY_NOTICES.md","web/assets.go","web/index.html","web/tsconfig.json","web/vite.config.ts","web/package.json","web/package-lock.json"]
 dirs=["api","cmd","internal","web/src","tools","deploy","licenses","evaluation",".github","docs"]
 result={ROOT/f for f in direct}
 for d in dirs:
  for f in (ROOT/d).rglob("*"):
   if not f.is_file():continue
   rel=f.relative_to(ROOT)
   if any(x in ("__pycache__","runs","node_modules") for x in rel.parts):continue
   if rel.parts[:2]==("docs","evidence") or str(rel) in ("docs/VERIFICATION.md","docs/PERFORMANCE.md"):continue
   if f.is_symlink():raise SystemExit("Symlink is not a release source input: "+str(rel))
   result.add(f)
 return sorted(result)
def freeze():
 files={f.relative_to(ROOT).as_posix():sha(f.read_bytes()) for f in inputs()}
 digest=sha(json.dumps(files,sort_keys=True,separators=(",",":")).encode())
 manifest={"software_version":VERSION,"source_input_sha256":digest,"scope":"public source/config/dependency/dataset/license inputs; generated evidence and web/dist excluded","files":files}
 (BUILD/"SOURCE_MANIFEST.json").write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+"\n")
 return manifest
def current():
 m=json.loads((BUILD/"SOURCE_MANIFEST.json").read_text())
 now={f.relative_to(ROOT).as_posix():sha(f.read_bytes()) for f in inputs()}
 if now!=m["files"]:raise SystemExit("Source changed after freeze. Re-freeze, rebuild, and rerun affected verification before packaging.")
 return m
if not a.package_only and not a.build_only:
 npm="npm.cmd" if os.name=="nt" else "npm"
 subprocess.run([npm,"run","build"],cwd=ROOT/"web",check=True)
 m=freeze()
else:m=current()
if a.freeze_only:
 print("Frozen source inputs:",m["source_input_sha256"]);raise SystemExit()
meta_path=BUILD/"BUILD_METADATA.json"
if not a.package_only:
 built=datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds").replace("+00:00","Z")
 flags="-s -w -X flowdesk/internal/buildinfo.SourceHash="+m["source_input_sha256"]+" -X flowdesk/internal/buildinfo.BuiltAt="+built
 go_version=subprocess.check_output([a.go,"version"],text=True).strip()
 binaries={}
 for target,name in (("linux","flowdesk"),("windows","flowdesk.exe")):
  env={**os.environ,"GOOS":target,"GOARCH":"amd64","CGO_ENABLED":"0"}
  subprocess.run([a.go,"build","-buildvcs=false","-trimpath","-ldflags",flags,"-o",str(BUILD/name),"./cmd/flowdesk"],cwd=ROOT,env=env,check=True)
  binaries[name]={"target":target+"/amd64","sha256":sha((BUILD/name).read_bytes())}
 for command in ("flowdesk-eval","flowdesk-bench"):
  env={**os.environ,"GOOS":"linux","GOARCH":"amd64","CGO_ENABLED":"0"}
  subprocess.run([a.go,"build","-buildvcs=false","-trimpath","-ldflags",flags,"-o",str(BUILD/command),"./cmd/"+command],cwd=ROOT,env=env,check=True)
 assets={f.relative_to(ROOT/"web/dist").as_posix():sha(f.read_bytes()) for f in sorted((ROOT/"web/dist").rglob("*")) if f.is_file()}
 metadata={"software_version":VERSION,"source_input_sha256":m["source_input_sha256"],"built_at":built,"go":go_version,"node":subprocess.check_output(["node","--version"],text=True).strip(),"frontend_assets_sha256":assets,"binaries":binaries,"go_mod_sha256":sha((ROOT/"go.mod").read_bytes()),"npm_lock_sha256":sha((ROOT/"web/package-lock.json").read_bytes()),"windows_native_verified":False}
 meta_path.write_text(json.dumps(metadata,ensure_ascii=False,indent=2)+"\n")
else:
 metadata=json.loads(meta_path.read_text())
 if metadata["source_input_sha256"]!=m["source_input_sha256"]:raise SystemExit("Binaries do not match source freeze.")
 for name,record in metadata["binaries"].items():
  if sha((BUILD/name).read_bytes())!=record["sha256"]:raise SystemExit("Binary hash mismatch.")
 for name,digest in metadata["frontend_assets_sha256"].items():
  if sha((ROOT/"web/dist"/name).read_bytes())!=digest:raise SystemExit("Frontend changed after embedded binary build.")
if a.build_only:print("Built both profiles/platform targets from",m["source_input_sha256"]);raise SystemExit()
a.out.mkdir(parents=True,exist_ok=True)
for target,binary in (("linux_amd64","flowdesk"),("windows_amd64","flowdesk.exe")):
 folder=f"FlowDesk_{VERSION}_{target}";archive=a.out/(folder+".zip")
 with zipfile.ZipFile(archive,"w",zipfile.ZIP_DEFLATED,compresslevel=9) as z:
  def writefile(path,rel,mode=0o644):
   info=zipfile.ZipInfo(folder+"/"+rel);info.external_attr=(0o100000|mode)<<16;info.compress_type=zipfile.ZIP_DEFLATED
   z.writestr(info,path.read_bytes(),compresslevel=9)
  writefile(BUILD/binary,binary,0o755)
  for name in ("LICENSE","THIRD_PARTY_NOTICES.md"):writefile(ROOT/name,name)
  for f in sorted((ROOT/"licenses").rglob("*")):
   if f.is_file():writefile(f,f.relative_to(ROOT).as_posix())
  for name in ("ARCHITECTURE.md","OPERATIONS.md","DEPLOYMENT.md","VERIFICATION.md","PERFORMANCE.md"):
   f=ROOT/"docs"/name
   if f.is_file():writefile(f,"docs/"+name)
  for f in sorted((ROOT/"docs/evidence").glob("*")):
   if f.is_file():writefile(f,"docs/evidence/"+f.name)
  writefile(ROOT/"docs/CHANGELOG.md","docs/CHANGELOG.md")
  writefile(ROOT/"evaluation/README.md","evaluation/README.md")
  writefile(ROOT/"api/openapi.json","api/openapi.json")
  writefile(meta_path,"BUILD_METADATA.json");writefile(BUILD/"SOURCE_MANIFEST.json","SOURCE_MANIFEST.json")
  readme=f"""FlowDesk {VERSION} — AI 工单协作与人工审核
{'Windows：解压后运行 启动FlowDesk.cmd。此包经过交叉编译，原生Windows验收待完成。' if target.startswith('windows') else 'Linux：chmod +x flowdesk run.sh，然后 ./run.sh。'}
使用者不需要安装 Go、Node、Python 或数据库。
若浏览器未打开，复制终端显示的 http://127.0.0.1:端口。保持终端，Ctrl+C 退出。
开始实际使用：运行“启动个人工作台.cmd”（Linux：./run-workspace.sh）。首次设置自己的管理员账号。
个人工作台使用独立目录，不创建演示账号、示例工单或资料。
原“启动FlowDesk.cmd”/run.sh 保留示例体验，原数据与历史不会被清空。
管理员 admin@flowdesk.demo / 工作人员 agent@flowdesk.demo / 客户 customer@flowdesk.demo
公开演示密码：FlowDeskDemo!2026
数据保存在本机当前用户的 FlowDesk 数据目录，与解压目录分离。
从0.1.0升级：关闭旧程序，解压新包并启动；继续使用原数据目录，不删除数据库或master.key。
Windows首次启动留下的空数据库会安全续建；已有工单与密钥不会重置。
启动失败时运行“故障诊断.cmd”（Linux使用 ./flowdesk --diagnose），查看只读诊断与 docs/OPERATIONS.md。
默认demo不调用模型。真实模型须由管理员配置并确认费用，发布回复仍须人工确认。
从工单生成草稿→核对来源→编辑/预览→确认发布→用客户账号查看回复。
再次生成在上方新增块，旧历史保留；失败提醒可查看详细引导和点X关闭。
docs/OPERATIONS.md：数据、备份、密码及密钥恢复。docs/VERIFICATION.md：已验证项与限制。
源码构建/评测工具在单独source包中；本运行包不含任何工作区、客户数据或模型密钥。
版本/来源：运行 flowdesk --version（Windows 使用 flowdesk.exe）。
source_input_sha256={m['source_input_sha256']}
"""
  z.writestr(folder+"/README.txt",readme)
  if target.startswith("windows"):
   z.writestr(folder+"/启动FlowDesk.cmd",'@echo off\r\nchcp 65001 >nul\r\ncd /d "%~dp0"\r\n"%~dp0flowdesk.exe" %*\r\nif errorlevel 1 (\r\n  echo.\r\n  echo FlowDesk failed to start. Read the code, data path and step above.\r\n  echo Run the other diagnostic CMD in this folder for a read-only report.\r\n  echo Do not delete your database or master.key.\r\n  pause\r\n  exit /b 1\r\n)\r\n')
   z.writestr(folder+"/启动个人工作台.cmd",'@echo off\r\nchcp 65001 >nul\r\ncd /d "%~dp0"\r\n"%~dp0flowdesk.exe" --workspace %*\r\nif errorlevel 1 (\r\n  echo.\r\n  echo FlowDesk failed to start. Keep your data and read the guidance above.\r\n  pause\r\n  exit /b 1\r\n)\r\n')
   z.writestr(folder+"/个人工作台故障诊断.cmd",'@echo off\r\nchcp 65001 >nul\r\ncd /d "%~dp0"\r\n"%~dp0flowdesk.exe" --workspace --diagnose %* > "%~dp0FlowDesk-workspace-diagnostic.txt" 2>&1\r\ntype "%~dp0FlowDesk-workspace-diagnostic.txt"\r\necho.\r\necho Review local paths before sharing this report.\r\npause\r\n')
   z.writestr(folder+"/故障诊断.cmd",'@echo off\r\nchcp 65001 >nul\r\ncd /d "%~dp0"\r\n"%~dp0flowdesk.exe" --diagnose %* > "%~dp0FlowDesk-startup-diagnostic.txt" 2>&1\r\ntype "%~dp0FlowDesk-startup-diagnostic.txt"\r\necho.\r\necho Report: "%~dp0FlowDesk-startup-diagnostic.txt"\r\necho This report may contain your local username/path. Review before sharing.\r\npause\r\n')
  else:
   info=zipfile.ZipInfo(folder+"/run.sh");info.external_attr=0o100755<<16;info.compress_type=zipfile.ZIP_DEFLATED
   z.writestr(info,'#!/bin/sh\nset -eu\ncd -- "$(dirname -- "$0")"\nexec ./flowdesk "$@"\n')
   info=zipfile.ZipInfo(folder+"/run-workspace.sh");info.external_attr=0o100755<<16;info.compress_type=zipfile.ZIP_DEFLATED
   z.writestr(info,'#!/bin/sh\nset -eu\ncd -- "$(dirname -- "$0")"\nexec ./flowdesk --workspace "$@"\n')
source=a.out/f"FlowDesk_{VERSION}_source.zip"
with zipfile.ZipFile(source,"w",zipfile.ZIP_DEFLATED,compresslevel=9) as z:
 selected=set(inputs())|{f for f in (ROOT/"web/dist").rglob("*") if f.is_file()}|{f for f in (ROOT/"docs").rglob("*") if f.is_file() and "__pycache__" not in f.parts}
 for f in sorted(selected):z.write(f,"flowdesk/"+f.relative_to(ROOT).as_posix())
 z.write(meta_path,"flowdesk/BUILD_METADATA.json");z.write(BUILD/"SOURCE_MANIFEST.json","flowdesk/SOURCE_MANIFEST.json")
checksums=[]
for f in sorted(a.out.glob(f"FlowDesk_{VERSION}_*.zip")):checksums.append(sha(f.read_bytes())+"  "+f.name)
(a.out/f"FlowDesk_{VERSION}_SHA256SUMS.txt").write_text("\n".join(checksums)+"\n")
print("Packaged:",", ".join(f.name for f in sorted(a.out.glob(f"FlowDesk_{VERSION}_*.zip"))))


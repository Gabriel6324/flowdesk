#!/usr/bin/env python3
"""Real process/API smoke. No browser automation, real provider or user workspace."""
import argparse,contextlib,hashlib,json,os,re,signal,socket,sqlite3,subprocess,tempfile,time,urllib.error,urllib.parse,urllib.request,uuid
from pathlib import Path
from release_version import VERSION
p=argparse.ArgumentParser();p.add_argument("--binary",type=Path,required=True);p.add_argument("--out",type=Path,required=True);p.add_argument("--upgrade-from",type=Path);a=p.parse_args()
binary=a.binary.resolve()
if not binary.is_file() or a.out.exists():raise SystemExit("Require executable and new result path.")
checks=[];children=[]
def mark(name):checks.append({"check":name,"status":"passed"})
def port():
 with socket.socket() as s:s.bind(("127.0.0.1",0));return s.getsockname()[1]
class Client:
 def __init__(self,address,origin=None):self.address=address;self.origin=origin or address;self.cookie=""
 def request(self,method,path,body=None,want=200,key=None):
  headers={"Host":urllib.parse.urlparse(self.origin).netloc}
  if self.cookie:headers["Cookie"]=self.cookie
  if body is not None:headers.update({"Content-Type":"application/json","Origin":self.origin})
  if key:headers["Idempotency-Key"]=key
  data=json.dumps(body,ensure_ascii=False).encode() if body is not None else None
  req=urllib.request.Request(self.address+path,data=data,method=method,headers=headers)
  try:resp=urllib.request.urlopen(req,timeout=6)
  except urllib.error.HTTPError as e:resp=e
  raw=resp.read();status=resp.status
  if status!=want:raise AssertionError(f"{method} {path}: expected {want}, got {status}: {raw[:250]!r}")
  if "Set-Cookie" in resp.headers:self.cookie=resp.headers["Set-Cookie"].split(";",1)[0]
  try:value=json.loads(raw)
  except (ValueError,UnicodeDecodeError):value=raw
  return value,resp.headers
 def data(self,*args,**kwargs):return self.request(*args,**kwargs)[0]["data"]
 def login(self,role="admin",password="FlowDeskDemo!2026"):return self.data("POST","/api/auth/login",{"email":role+"@flowdesk.demo","password":password})
def start(data,profile="desktop",origin=None,executable=None):
 pt=port();base=f"http://127.0.0.1:{pt}";log=data.parent/(data.name+".process.log");fp=log.open("wb")
 args=[str(executable or binary),"--no-browser","--data-dir",str(data),"--listen",f"127.0.0.1:{pt}","--profile",profile]
 if origin:args+=["--public-origin",origin]
 proc=subprocess.Popen(args,stdout=fp,stderr=subprocess.STDOUT);children.append((proc,fp));client=Client(base,origin)
 deadline=time.monotonic()+20
 while time.monotonic()<deadline:
  if proc.poll() is not None:raise AssertionError(f"process stopped: {log.read_text(encoding='utf-8',errors='replace')[-700:]}")
  try:client.request("GET","/api/ready");return proc,client
  except (urllib.error.URLError,TimeoutError):time.sleep(.05)
 raise AssertionError("startup deadline")
def stop(proc):
 if proc.poll() is None:
  if os.name=="nt":proc.terminate()
  else:proc.send_signal(signal.SIGINT)
  try:code=proc.wait(timeout=12)
  except subprocess.TimeoutExpired:proc.kill();proc.wait();raise AssertionError("graceful shutdown deadline")
  if os.name!="nt" and code!=0:raise AssertionError(f"shutdown exit {code}")
 for child,fp in children:
  if child is proc and proc.poll() is not None:fp.close()
def offline(data,*flags,stdin=None,want=0):
 result=subprocess.run([str(binary),"--data-dir",str(data),*flags],input=stdin,text=True,encoding="utf-8",capture_output=True,timeout=20)
 if result.returncode!=want:raise AssertionError(f"offline command failed {flags}: {result.stderr[-500:]}")
 return result
def cleanup_children():
 for proc,fp in children:
  try:
   if proc.poll() is None:
    proc.kill();proc.wait(timeout=5)
  finally:fp.close()
try:
 with tempfile.TemporaryDirectory(prefix="flowdesk-smoke-") as temp, contextlib.ExitStack() as cleanup:
  cleanup.callback(cleanup_children)
  root=Path(temp);data=root/"workspace with space";proc,admin=start(data);admin.login()
  health=admin.data("GET","/api/health");assert health["version"]==VERSION and health["profile"]=="desktop";mark("desktop startup and health")
  html,headers=admin.request("GET","/");assert isinstance(html,bytes) and b"FlowDesk" in html and headers.get("Content-Security-Policy");assets={}
  for asset in re.findall(rb'(?:src|href)="(/assets/[^"]+)"',html):
   path=asset.decode();raw,h=admin.request("GET",path);assert isinstance(raw,bytes) and raw;assets[path]=hashlib.sha256(raw).hexdigest()
  assert len(assets)>=2;mark("embedded HTML JavaScript CSS and security headers")
  customer=Client(admin.address);customer.login("customer")
  key=uuid.uuid4().hex;body={"title":"API 401 process smoke","description":"测试环境接口认证失败。请协助安全排查。","category":"other","priority":"normal"}
  t=customer.data("POST","/api/tickets",body,want=201,key=key)
  replay=customer.request("POST","/api/tickets",body,want=201,key=key)[0];assert replay["data"]["id"]==t["id"] and replay["replayed"];mark("real HTTP create and idempotent replay")
  path="/api/tickets/"+t["id"]
  admin.data("POST",path+"/comments",{"expected_revision":t["revision"],"visibility":"internal","body":"staff-only process note"},want=201,key=uuid.uuid4().hex)
  public=customer.data("GET",path);assert not public["comments"] and "active_job_id" not in public["ticket"];mark("requester excludes internal note and task pointer")
  revision=admin.data("GET",path)["ticket"]["revision"]
  job=admin.data("POST",path+"/jobs",{"expected_revision":revision,"strategy":"workflow","expected_config_version":1},want=202,key=uuid.uuid4().hex)
  deadline=time.monotonic()+12
  while time.monotonic()<deadline:
   detail=admin.data("GET","/api/jobs/"+job["id"])
   if detail["status"]=="succeeded":break
   assert detail["status"] in ("queued","running");time.sleep(.03)
  assert detail["status"]=="succeeded" and detail["model_calls"]==[]
  drafts=admin.data("GET",path+"/drafts");d=drafts[0];assert d["job_id"]==job["id"];mark("demo durable job, source snapshot and zero provider calls")
  revision=admin.data("GET",path)["ticket"]["revision"]
  admin.data("POST",path+"/apply-suggestion",{"expected_revision":revision,"draft_id":d["id"],"apply_category":True,"apply_priority":False},key=uuid.uuid4().hex)
  revision=admin.data("GET",path)["ticket"]["revision"];drafts=admin.data("GET",path+"/drafts");d=drafts[0]
  exact="  人工核对后的正式回复\n保留原始空白。 "
  admin.data("POST",path+"/approve",{"expected_revision":revision,"draft_id":d["id"],"reply_text":exact,"expected_review_context":d["review_context"]["token"],"acknowledgements":d["review_context"]["required_acknowledgements"]},key=uuid.uuid4().hex)
  public=customer.data("GET",path);assert public["ticket"]["status"]=="resolved" and public["replies"][-1]["body"]==exact;mark("suggestion selection and exact human-reviewed public reply")
  original_cookie=admin.cookie;lock=offline(data,"--no-browser",want=1);assert "INSTANCE_LOCK_FAILED" in lock.stderr;mark("exclusive process lock")
  stop(proc);mark("process stopped before DB close (Linux graceful; Windows terminate only)")
  backup=offline(data,"--backup");groups=list((data/"backups").glob("backup-*"));assert groups
  meta=json.loads((groups[-1]/"manifest.json").read_text(encoding="utf-8"));assert meta["software_version"]==VERSION;assert meta["database_sha256"]==hashlib.sha256((groups[-1]/"flowdesk.db").read_bytes()).hexdigest();assert (groups[-1]/"master.key").stat().st_size==32;mark("offline consistent paired backup and version manifest")
  proc,admin=start(data);admin.cookie=original_cookie
  after=admin.data("GET",path);assert after["replies"][-1]["body"]==exact;mark("restart preserves workspace, session and immutable public history")
  revision=after["ticket"]["revision"];reopened=admin.data("POST",path+"/reopen",{"expected_revision":revision},key=uuid.uuid4().hex);assert reopened["cycle_no"]==2
  for _ in range(2):
   revision=admin.data("GET",path)["ticket"]["revision"];job=admin.data("POST",path+"/jobs",{"expected_revision":revision},want=202,key=uuid.uuid4().hex)
   deadline=time.monotonic()+12
   while time.monotonic()<deadline:
    j=admin.data("GET","/api/jobs/"+job["id"])
    if j["status"]=="succeeded":break
    time.sleep(.02)
   assert j["status"]=="succeeded"
  history=admin.data("GET",path+"/drafts");assert len(history)==3 and history[0]["job_id"]==job["id"] and history[-1]["id"]==d["id"];mark("new generation prepends blocks and preserves prior cycle history")
  exported,h=admin.request("GET","/api/tickets/export?format=csv");assert isinstance(exported,bytes) and "text/csv" in h["Content-Type"];mark("filtered CSV HTTP export")
  stop(proc)
  reset=offline(data,"--reset-admin-password",stdin="admin@flowdesk.demo\nSmokeNewPassword!2026\nSmokeNewPassword!2026\n")
  proc,admin=start(data);admin.cookie=original_cookie;admin.request("GET","/api/auth/me",want=401);admin.cookie="";admin.login(password="SmokeNewPassword!2026");mark("offline password reset invalidates old session")
  stop(proc);(data/"master.key").unlink();proc,admin=start(data);admin.login(password="SmokeNewPassword!2026");assert admin.data("GET",path)["replies"][-1]["body"]==exact;assert not admin.data("GET","/api/settings/ai")["keystore_available"];mark("missing key preserves ordinary business reads and reports degraded key health")
  stop(proc);offline(data,"--recover-keystore");assert (data/"master.key").stat().st_size==32 and not (data/"keystore-recovery.json").exists();mark("offline keystore recovery preserves business database")
  mismatch=offline(data,"--profile","server","--no-browser","--public-origin","https://flowdesk.example.test",want=1);assert "PROFILE_MISMATCH" in mismatch.stderr;mark("server rejects desktop workspace")
  serverdata=root/"server workspace"
  uninit=offline(serverdata,"--profile","server","--no-browser","--public-origin","https://flowdesk.example.test",want=1);assert "SERVER_BOOTSTRAP_REQUIRED" in uninit.stderr
  offline(serverdata,"--profile","server","--init-admin",stdin="owner@example.test\nSmoke owner\nServerSmokePassword!2026\nServerSmokePassword!2026\n")
  with contextlib.closing(sqlite3.connect(serverdata/"flowdesk.db")) as db, db:assert db.execute("SELECT COUNT(*) FROM users").fetchone()[0]==1 and db.execute("SELECT COUNT(*) FROM users WHERE email LIKE '%@flowdesk.demo'").fetchone()[0]==0
  proc,serverclient=start(serverdata,"server","https://flowdesk.example.test")
  value,headers=serverclient.request("POST","/api/auth/login",{"email":"owner@example.test","password":"ServerSmokePassword!2026"});assert value["data"]["profile"]=="server";sc=headers["Set-Cookie"];assert sc.startswith("__Host-") and "Secure" in sc and "HttpOnly" in sc and "SameSite=Strict" in sc
  ready=subprocess.run([str(binary),"--healthcheck",serverclient.address+"/api/ready","--public-origin","https://flowdesk.example.test"],capture_output=True,timeout=6);assert ready.returncode==0;mark("server bootstrap, fixed Host, secure cookie attributes and CLI readiness")
  stop(proc)
  missing=root/"not-created diagnostic"
  report=offline(missing,"--diagnose");assert "尚未创建" in report.stdout and not missing.exists();mark("read-only diagnosis never creates a missing workspace")
  pristine=root/"中断初始化 中文路径 #% &'"
  pristine.mkdir();(pristine/"flowdesk.db").write_bytes(b"");old_key=b"k"*32;(pristine/"master.key").write_bytes(old_key)
  report=offline(pristine,"--diagnose");assert "没有业务数据" in report.stdout and (pristine/"flowdesk.db").stat().st_size==0
  for flag in ("--backup","--reset-admin-password","--recover-keystore"):
   result=offline(pristine,flag,want=1);assert "WORKSPACE_NOT_FOUND" in result.stderr and (pristine/"flowdesk.db").stat().st_size==0 and (pristine/"master.key").read_bytes()==old_key
  mark("offline commands refuse to initialize an empty workspace")
  proc,client=start(pristine);client.login();assert client.data("GET","/api/health")["version"]==VERSION and (pristine/"master.key").read_bytes()==old_key
  stop(proc);proc,client=start(pristine);client.login()
  with contextlib.closing(sqlite3.connect(pristine/"flowdesk.db")) as db, db:assert db.execute("SELECT COUNT(*) FROM users").fetchone()[0]==3 and db.execute("SELECT COUNT(*) FROM tickets").fetchone()[0]==4
  stop(proc);mark("interrupted first launch resumes in Unicode/special-character path without resetting key or duplicating seed")
  foreign=root/"foreign workspace";foreign.mkdir()
  with contextlib.closing(sqlite3.connect(foreign/"flowdesk.db")) as db, db:db.execute("CREATE TABLE valuable_data(value TEXT)");db.execute("INSERT INTO valuable_data VALUES('preserve')")
  before=(foreign/"flowdesk.db").read_bytes();result=offline(foreign,"--no-browser",want=1);assert "SCHEMA_INVALID" in result.stderr and "失败步骤" in result.stderr and str(foreign) in result.stderr and (foreign/"flowdesk.db").read_bytes()==before and not (foreign/"master.key").exists();mark("foreign schema rejected with stage/path and no database/key rewrite")
  damaged=root/"damaged workspace";damaged.mkdir();(damaged/"flowdesk.db").write_bytes(b"this is not a SQLite database")
  before=(damaged/"flowdesk.db").read_bytes();result=offline(damaged,"--diagnose",want=1);assert "DATABASE_CORRUPT" in result.stderr and "SQLite错误编号" in result.stderr and (damaged/"flowdesk.db").read_bytes()==before;mark("corrupt file gets corruption guidance instead of schema-version guidance")
  upgrade_version=None
  if a.upgrade_from:
   old=a.upgrade_from.resolve();upgrade_version=subprocess.check_output([str(old),"--version"],text=True,encoding="utf-8").strip();assert upgrade_version.startswith("FlowDesk 0.1.0;")
   upgraded=root/"0.1.0升级 中文路径";proc,old_client=start(upgraded,executable=old);old_client.login();cookie=old_client.cookie
   body={"title":"Preserve existing 0.1.0 ticket","description":"升级时保留这条已有工单。","category":"other","priority":"normal"}
   t=old_client.data("POST","/api/tickets",body,want=201,key=uuid.uuid4().hex);path="/api/tickets/"+t["id"]
   reply="旧版本已经发布的人工回复。"
   old_client.data("POST",path+"/reply",{"expected_revision":t["revision"],"reply_text":reply},key=uuid.uuid4().hex)
   stop(proc);key=(upgraded/"master.key").read_bytes()
   with contextlib.closing(sqlite3.connect(upgraded/"flowdesk.db")) as db, db:workspace=db.execute("SELECT workspace_id FROM workspace_meta").fetchone()[0]
   proc,client=start(upgraded);client.cookie=cookie;current=client.data("GET",path)
   assert current["ticket"]["description"]==body["description"] and current["replies"][-1]["body"]==reply and (upgraded/"master.key").read_bytes()==key
   with contextlib.closing(sqlite3.connect(upgraded/"flowdesk.db")) as db, db:assert db.execute("SELECT workspace_id FROM workspace_meta").fetchone()[0]==workspace
   stop(proc);mark("real 0.1.0 workspace upgrades preserving session, ticket, published reply, workspace identity and key")
  version=subprocess.check_output([str(binary),"--version"],text=True,encoding="utf-8").strip()
  a.out.parent.mkdir(parents=True,exist_ok=True);a.out.write_text(json.dumps({"version_output":version,"checks":checks,"embedded_assets_sha256":assets,"scope":"native process/API only; no real browser/provider","upgrade_from_version":upgrade_version,"windows_graceful_exit_verified":False},ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
  print(f"Process/API smoke passed: {len(checks)} checks. {a.out}")
finally:
 cleanup_children()


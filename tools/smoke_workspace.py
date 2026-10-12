#!/usr/bin/env python3
"""Exercise personal accounts and the real dispatch path against a local fake provider."""
import argparse,contextlib,hashlib,json,signal,socket,sqlite3,subprocess,tempfile,threading,time,urllib.error,urllib.request,uuid
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from pathlib import Path

p=argparse.ArgumentParser();p.add_argument('--binary',type=Path,required=True);p.add_argument('--out',type=Path,required=True);p.add_argument('--upgrade-from',type=Path);a=p.parse_args()
binary=a.binary.resolve();checks=[];children=[];snapshots=0
if not binary.is_file() or a.out.exists():raise SystemExit('Require a binary and a new output path')
def mark(name):checks.append({'check':name,'status':'passed'})
def port():
 with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
class Client:
 def __init__(self,base):self.base=base;self.cookie=''
 def call(self,method,path,body=None,want=200):
  headers={'Origin':self.base,'Content-Type':'application/json'}
  if self.cookie:headers['Cookie']=self.cookie
  if body is not None:headers['Idempotency-Key']=uuid.uuid4().hex
  req=urllib.request.Request(self.base+path,method=method,headers=headers,data=json.dumps(body).encode() if body is not None else None)
  try:r=urllib.request.urlopen(req,timeout=8)
  except urllib.error.HTTPError as err:r=err
  raw=r.read();assert r.status==want,(method,path,r.status,raw[:180])
  if r.headers.get('Set-Cookie'):self.cookie=r.headers['Set-Cookie'].split(';')[0]
  return json.loads(raw)
 def data(self,*args,**kwargs):return self.call(*args,**kwargs)['data']
 def login(self,email,password):return self.data('POST','/api/auth/login',{'email':email,'password':password})
def start(data,workspace=False,credentials=None,exe=None):
 base='http://127.0.0.1:'+str(port());log=data.parent/(data.name+'.process.log');fp=log.open('wb')
 args=[str(exe or binary),'--data-dir',str(data),'--listen',base.removeprefix('http://'),'--no-browser']
 if workspace:args+=['--workspace']
 proc=subprocess.Popen(args,stdin=subprocess.PIPE,stdout=fp,stderr=subprocess.STDOUT);children.append((proc,fp))
 if credentials:proc.stdin.write(credentials.encode());proc.stdin.flush()
 proc.stdin.close();client=Client(base);deadline=time.monotonic()+20
 while time.monotonic()<deadline:
  if proc.poll() is not None:raise AssertionError('Application stopped: '+log.read_text()[-700:])
  try:client.data('GET','/api/ready');return proc,client
  except (urllib.error.URLError,TimeoutError):time.sleep(.03)
 raise AssertionError('startup deadline')
def stop(proc):
 if proc.poll() is None:
  proc.send_signal(signal.SIGINT);assert proc.wait(timeout=12)==0
 for child,fp in children:
  if child is proc:fp.close()
def snapshot(client,ticket):
 global snapshots
 view=client.data('GET','/api/tickets/'+ticket);snapshots+=1
 work=view['work'];active=view['ticket'].get('active_job_id')
 if active:
  matching=[j for j in work['jobs'] if j['id']==active]
  assert len(matching)==1 and matching[0]['status'] in ('queued','running'),'terminal job remains active'
 return view
def generate(client,ticket,version,strategy='workflow',expected='succeeded'):
 view=snapshot(client,ticket)
 job=client.data('POST','/api/tickets/'+ticket+'/jobs',{'expected_revision':view['ticket']['revision'],'strategy':strategy,'expected_config_version':version},want=202)
 deadline=time.monotonic()+12
 while time.monotonic()<deadline:
  view=snapshot(client,ticket);current=next(j for j in view['work']['jobs'] if j['id']==job['id'])
  if current['status'] not in ('queued','running'):break
  time.sleep(.01)
 assert current['status']==expected and not view['ticket'].get('active_job_id'),current
 if expected=='succeeded':assert any(d['job_id']==job['id'] for d in view['work']['drafts'])
 return current,view
def evidence_ids(value):
 result=[]
 if isinstance(value,dict):
  if isinstance(value.get('evidence_id'),str):result.append(value['evidence_id'])
  for v in value.values():result+=evidence_ids(v)
 elif isinstance(value,list):
  for v in value:result+=evidence_ids(v)
 elif isinstance(value,str):
  try:result+=evidence_ids(json.loads(value))
  except (ValueError,TypeError):pass
 return result
class Provider(BaseHTTPRequestHandler):
 calls=0;fail_next=False
 def log_message(self,*args):pass
 def do_POST(self):
  Provider.calls+=1
  assert self.path=='/v1/chat/completions' and self.headers['Authorization']=='Bearer local-protocol-test-key'
  request=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  assert request['max_tokens']<=4096
  if Provider.fail_next:
   Provider.fail_next=False;self.send_response(429);self.end_headers();self.wfile.write(b'{"error":{"code":"rate_limit_exceeded"}}');return
  messages=request['messages'];tools=request.get('tools',[])
  if 'basic connection test' in json.dumps(messages):content={'ok':True};message={'content':json.dumps(content)};finish='stop'
  elif tools and not any(m['role']=='tool' for m in messages):
   message={'content':None,'tool_calls':[{'id':'ticket-call','type':'function','function':{'name':'get_current_ticket','arguments':'{}'}},{'id':'kb-call','type':'function','function':{'name':'search_knowledge','arguments':json.dumps({'query':'连接失败','limit':3})}}]};finish='tool_calls'
  else:
   content={'summary':'连接失败排查','suggested_category':'technical','suggested_priority':'normal','reply_text':'请按公开说明重启客户端后重新连接；如仍失败，请提供发生时间和不含密钥的请求 ID。','evidence_ids':sorted(set(evidence_ids(messages))),'needs_more_information':True}
   message={'content':json.dumps(content,ensure_ascii=False)};finish='stop'
  body=json.dumps({'choices':[{'message':message,'finish_reason':finish}],'usage':{'prompt_tokens':11,'completion_tokens':22}}).encode()
  self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(body)
try:
 with tempfile.TemporaryDirectory(prefix='flowdesk-personal-smoke-') as tmp:
  root=Path(tmp);data=root/'个人工作台 data';proc,admin=start(data,True,'owner@example.test\nOwner\nPersonalStrong!2026\nPersonalStrong!2026\n')
  health=admin.data('GET','/api/health');assert health['workspace_mode']=='personal' and not health['demo_accounts_available'];mark('first personal launch creates own account and hides public quick-login credentials')
  admin.login('owner@example.test','PersonalStrong!2026')
  view=admin.data('GET','/api/dashboard');assert view['total']==0 and view['setup']=={'knowledge_articles':0,'requester_accounts':0,'staff_accounts':1,'model_configured':False};mark('personal workspace starts without sample tickets or knowledge and reports truthful setup')
  bad=Client(admin.base);bad.call('POST','/api/auth/login',{'email':'admin@flowdesk.demo','password':'FlowDeskDemo!2026'},want=401);mark('public demo password cannot enter personal workspace')
  admin.call('POST','/api/users',{'email':'agent@flowdesk.demo','display_name':'Reserved','role':'agent','password':'PersonalStrong!2026'},want=400);mark('personal user creation rejects reserved public demo accounts')
  for email,role in [('staff@example.test','agent'),('requester@example.test','requester')]:
   admin.data('POST','/api/users',{'email':email,'display_name':role,'role':role,'password':'PersonalStrong!2026'},want=201)
  staff=Client(admin.base);staff.login('staff@example.test','PersonalStrong!2026');customer=Client(admin.base);customer.login('requester@example.test','PersonalStrong!2026');mark('own staff and requester accounts authenticate with separate roles')
  admin.data('POST','/api/knowledge',{'title':'连接失败排查','body':'连接失败时先重启客户端后重新连接。仍失败时提供发生时间和请求 ID，不提供密钥。','tags':['连接','排查']},want=201)
  customer.call('GET','/api/knowledge',want=403);mark('real public knowledge is stored and remains staff-only')
  t=customer.data('POST','/api/tickets',{'title':'连接失败','description':'客户端无法连接服务，请协助排查连接失败。','category':'technical','priority':'normal'},want=201);ticket=t['id'];mark('own requester submits an actual persistent ticket')
  server=ThreadingHTTPServer(('127.0.0.1',0),Provider);thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
  with contextlib.ExitStack() as stack:
   stack.callback(server.server_close);stack.callback(server.shutdown)
   cfg=admin.data('GET','/api/settings/ai')
   def configure(strategy,key=''):
    global cfg
    cfg=admin.data('PUT','/api/settings/ai',{'mode':'live','strategy':strategy,'base_url':'http://127.0.0.1:'+str(server.server_port)+'/v1','model':'local-protocol-mock','timeout_seconds':30,'output_token_limit':1024,'pricing':None,'config_version':cfg['config_version'],'api_key':key})
    assert 'local-protocol-test-key' not in json.dumps(cfg)
   configure('workflow','local-protocol-test-key');assert admin.data('GET','/api/dashboard')['setup']['model_configured'];mark('encrypted live settings persist and readiness says configured rather than proven quality')
   test=admin.data('POST','/api/settings/ai/test',{});assert test['basic_call_succeeded'] and not test['tools_verified'];mark('native program sends a basic connection test through the local provider')
   view=snapshot(staff,ticket);before=Provider.calls
   result=staff.call('POST','/api/tickets/'+ticket+'/jobs',{'expected_revision':view['ticket']['revision']},want=400)
   assert result['error']['code']=='LIVE_CONSENT_REQUIRED' and Provider.calls==before;mark('missing live fee/configuration confirmation creates no task or provider call')
   j,view=generate(admin,ticket,cfg['config_version']);assert j['provider_mode']=='live' and len(j['model_calls'])==1 and j['model_calls'][0]['usage_complete'];mark('workflow uses live dispatch with persisted usage and one provider request')
   d=view['work']['drafts'][0];reply='核对后的正式回复：请重启客户端后重新连接。'
   admin.data('POST','/api/tickets/'+ticket+'/approve',{'expected_revision':view['ticket']['revision'],'draft_id':d['id'],'reply_text':reply,'expected_review_context':d['review_context']['token'],'acknowledgements':d['review_context']['required_acknowledgements']})
   visible=customer.data('GET','/api/tickets/'+ticket);assert visible['replies'][0]['body']==reply and 'work' not in visible;mark('human-approved reply is exact and requester never receives staff job/draft snapshot')
   admin.data('POST','/api/tickets/'+ticket+'/reopen',{'expected_revision':visible['ticket']['revision']});configure('agent')
   j,view=generate(staff,ticket,cfg['config_version'],'agent');assert len(j['model_calls'])==2 and j['execution_strategy']=='agent';mark('staff agent uses real tool-dispatch protocol and completes after two local model calls')
   old_drafts=len(view['work']['drafts']);Provider.fail_next=True;before=Provider.calls
   j,view=generate(admin,ticket,cfg['config_version'],'workflow','failed')
   assert j['error_code']=='PROVIDER_RATE_LIMIT' and len(view['work']['drafts'])==old_drafts and Provider.calls==before+1;mark('provider failure has precise guide code, keeps old results and does not retry automatically')
   for actor in (admin,staff):
    for _ in range(20):generate(actor,ticket,cfg['config_version'],'workflow')
   mark('forty rapid generations across admin and staff keep job, pointer and new result consistent')
   old_key=(data/'master.key').read_bytes();calls=Provider.calls;cookie=customer.cookie;stop(proc)
   proc,admin=start(data,True);admin.login('owner@example.test','PersonalStrong!2026');customer=Client(admin.base);customer.cookie=cookie
   visible=customer.data('GET','/api/tickets/'+ticket)
   assert visible['replies'][0]['body']==reply and (data/'master.key').read_bytes()==old_key and Provider.calls==calls;mark('personal restart preserves session, key, accounts, published reply and makes no model replay')
   stop(proc)
   with sqlite3.connect(data/'flowdesk.db') as db:
    assert db.execute('SELECT COUNT(*) FROM users').fetchone()[0]==3 and db.execute('SELECT COUNT(*) FROM knowledge_articles').fetchone()[0]==1 and db.execute('SELECT COUNT(*) FROM jobs WHERE encrypted_secret IS NOT NULL').fetchone()[0]==0
   mark('private accounts are not reseeded and completed jobs retain no encrypted secret snapshots')
  upgrade_version=None
  if a.upgrade_from:
   old=a.upgrade_from.resolve();upgrade_version=subprocess.check_output([str(old),'--version'],text=True).strip();assert upgrade_version.startswith('FlowDesk 0.1.1;')
   old_data=root/'upgrade 0.1.1';proc,old_admin=start(old_data,exe=old);old_admin.login('admin@flowdesk.demo','FlowDeskDemo!2026')
   t=old_admin.data('POST','/api/tickets',{'title':'Preserve 0.1.1 ticket','description':'retain this old ticket','category':'other','priority':'normal'},want=201)
   old_admin.data('POST','/api/tickets/'+t['id']+'/reply',{'expected_revision':t['revision'],'reply_text':'Existing published reply'});cookie=old_admin.cookie;stop(proc);key=(old_data/'master.key').read_bytes()
   proc,current=start(old_data);current.cookie=cookie;v=current.data('GET','/api/tickets/'+t['id'])
   assert v['replies'][0]['body']=='Existing published reply' and (old_data/'master.key').read_bytes()==key;stop(proc);mark('genuine 0.1.1 executable upgrades without replacing existing reply, session or key')
  result={'version_output':subprocess.check_output([str(binary),'--version'],text=True).strip(),'checks':checks,'consistent_snapshot_reads':snapshots,'local_mock_provider_requests':Provider.calls,'upgrade_from_version':upgrade_version,'scope':'native Linux process/API; local fake provider only; no real model quality claim'}
  a.out.parent.mkdir(parents=True,exist_ok=True);a.out.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print('Personal workspace/API checks passed:',len(checks))
finally:
 for proc,fp in children:
  if proc.poll() is None:proc.kill();proc.wait(timeout=5)
  fp.close()

from pathlib import Path
from release_version import VERSION as SOFTWARE_VERSION
import json
ROOT=Path(__file__).resolve().parents[1]
def string(**kw): return dict(type="string",**kw)
def integer(**kw): return dict(type="integer",**kw)
def number(**kw): return dict(type="number",**kw)
def boolean(**kw): return dict(type="boolean",**kw)
def ref(name,**kw): return dict({"$ref":"#/components/schemas/"+name},**kw)
def array(items,**kw): return dict(type="array",items=items,**kw)
def obj(props,optional=()): return dict(type="object",properties=props,required=[k for k in props if k not in optional],additionalProperties=False)
def enum(*v): return string(enum=list(v))
ID=string(pattern=r"^[1-9][0-9]*$")
DATE=string(format="date-time")
NULL_ID=dict(ID,nullable=True)
NULL_STR=string(nullable=True)
CATEGORY=enum("account","billing","technical","other")
PRIORITY=enum("low","normal","high","urgent")
STATUS=enum("open","in_progress","awaiting_review","resolved")
VERSION={"content_version":integer(minimum=1),"conversation_version":integer(minimum=1),"cycle_no":integer(minimum=1)}
S={}
S["User"]=obj(dict(id=ID,email=string(maxLength=254),display_name=string(maxLength=100),role=enum("admin","agent","requester"),disabled=boolean(),revision=integer(minimum=1),authorization_version=integer(minimum=1)))
S["Me"]=obj(dict(S["User"]["properties"],workspace_id=string(),profile=enum("desktop","server")))
S["Ticket"]=obj(dict(id=ID,display_id=string(),title=string(maxLength=200),description=string(maxLength=6000),requester_id=ID,requester_name=string(),assignee_id=NULL_ID,assignee_name=NULL_STR,category=CATEGORY,priority=PRIORITY,status=STATUS,revision=integer(minimum=1),**VERSION,active_job_id=ID,approved_reply_id=NULL_ID,created_at=DATE,updated_at=DATE,resolved_at=string(format="date-time",nullable=True)),["active_job_id"])
S["Comment"]=obj(dict(id=ID,ticket_id=ID,cycle_no=integer(),author_name=string(),visibility=enum("public","internal"),body=string(maxLength=3000),created_at=DATE))
S["Reply"]=obj(dict(id=ID,cycle_no=integer(),body=string(maxLength=6000),author_name=string(),created_at=DATE))
S["TicketWork"]=obj(dict(drafts=array(ref("Draft")),jobs=array(ref("Job"),maxItems=10)))
S["TicketDetail"]=obj(dict(ticket=ref("Ticket"),comments=array(ref("Comment")),replies=array(ref("Reply")),work=ref("TicketWork")),["work"])
S["Article"]=obj(dict(id=ID,display_id=string(),title=string(maxLength=200),body=string(maxLength=20000),tags=array(string(maxLength=50),maxItems=10),version=integer(minimum=1),archived=boolean(),updated_at=DATE))
S["Source"]=obj(dict(article_id=ID,evidence_id=string(),version=integer(minimum=1),title=string(),excerpt=string(maxLength=1200),body_hash=string()))
S["SourceHead"]=obj(dict(article_id=ID,version=integer(minimum=1),archived=boolean()))
S["ReviewContext"]=obj(dict(token=string(),heads=array(ref("SourceHead")),required_acknowledgements=array(enum("no_evidence","sources_changed","context_omitted"))))
S["AIOutput"]=obj(dict(summary=string(maxLength=500),suggested_category=CATEGORY,suggested_priority=PRIORITY,reply_text=string(maxLength=6000),evidence_ids=array(string()),needs_more_information=boolean()))
S["Draft"]=obj(dict(id=ID,job_id=ID,ticket_id=ID,**VERSION,provider_mode=enum("demo","live"),**S["AIOutput"]["properties"],context_omitted=boolean(),warning_codes=array(string()),sources=array(ref("Source")),review_state=enum("pending","approved","rejected"),review_context=ref("ReviewContext"),valid=boolean(),created_at=DATE))
S["Pricing"]=obj(dict(input_per_million=number(minimum=0),output_per_million=number(minimum=0),currency=enum("USD","CNY")))
S["AIConfig"]=obj(dict(mode=enum("demo","live"),strategy=enum("workflow","agent"),base_url=string(),model=string(maxLength=200),timeout_seconds=integer(minimum=5,maximum=120),output_token_limit=integer(minimum=256,maximum=4096),pricing={"allOf":[ref("Pricing")],"nullable":True},config_version=integer(minimum=1)))
S["Settings"]=obj(dict(S["AIConfig"]["properties"],has_api_key=boolean(),masked_key=string(),keystore_available=boolean(),live_allowed=boolean()))
S["Call"]=obj(dict(call_ordinal=integer(),status=string(),dispatch_confirmed=boolean(),duration_ms=integer(),input_tokens=integer(nullable=True),output_tokens=integer(nullable=True),cached_tokens=integer(nullable=True),usage_complete=boolean(),error_code=NULL_STR))
S["Step"]=obj(dict(ordinal=integer(),kind=string(),state=string(),started_at=DATE,finished_at=string(nullable=True)))
S["RuntimeHint"]=obj(dict(storage_blocked=boolean(),intended_terminal_state=string(),pending_result_available=boolean(),error_code=string()))
S["Job"]=obj(dict(id=ID,ticket_id=ID,actor_id=ID,parent_job_id=NULL_ID,provider_mode=enum("demo","live"),execution_strategy=enum("workflow","agent"),configuration=ref("AIConfig"),status=enum("queued","running","succeeded","failed","canceled","stale"),error_code=NULL_STR,error_stage=NULL_STR,request_id=string(),created_at=DATE,started_at=string(nullable=True),finished_at=string(nullable=True),steps=array(ref("Step")),model_calls=array(ref("Call")),runtime_hint=ref("RuntimeHint")),["runtime_hint"])
S["Audit"]=obj(dict(id=ID,actor_name=string(),action=string(),details=dict(type="object",additionalProperties=True),created_at=DATE))
S["Assignee"]=obj(dict(id=ID,display_name=string()))
S["WorkspaceReadiness"]=obj(dict(knowledge_articles=integer(minimum=0),requester_accounts=integer(minimum=0),staff_accounts=integer(minimum=0),model_configured=boolean()))
S["Dashboard"]=obj({**{k:integer(minimum=0) for k in ("total","open","in_progress","awaiting_review","resolved","generated","demo_generated","live_generated","approved","rejected","manual")},"assignees":array(ref("Assignee")),"ai_mode":enum("demo","live"),"ai_strategy":enum("workflow","agent"),"storage_blocked":boolean(),"ai_config_version":integer(minimum=1),"workspace_mode":enum("demo","personal","server"),"setup":ref("WorkspaceReadiness")},["assignees","ai_mode","ai_strategy","storage_blocked","ai_config_version","workspace_mode","setup"])
S["Error"]=obj(dict(code=string(),message=string(),stage=string(),retryable=boolean(),guide_id=string()),["stage"])
S["ErrorEnvelope"]=obj(dict(error=ref("Error"),request_id=string()))
for name in ("Ticket","Article","Job"): S[name+"Page"]=obj(dict(items=array(ref(name)),page=integer(),page_size=integer(),total=integer()))
S["Empty"]=obj({})
S["LoginBody"]=obj(dict(email=string(maxLength=254),password=string(maxLength=128)))
S["PasswordBody"]=obj(dict(current_password=string(maxLength=128),new_password=string(minLength=12,maxLength=128)))
S["TicketCreate"]=obj(dict(title=string(minLength=1,maxLength=200),description=string(minLength=1,maxLength=6000),category=CATEGORY,priority=PRIORITY))
S["TicketPatch"]=obj(dict(expected_revision=integer(minimum=1),title=string(maxLength=200),description=string(maxLength=6000),category=CATEGORY,priority=PRIORITY,status=STATUS,assignee_id=ID,clear_assignee=boolean()),["title","description","category","priority","status","assignee_id","clear_assignee"])
S["VersionBody"]=obj(dict(expected_revision=integer(minimum=1)))
S["CommentBody"]=obj(dict(expected_revision=integer(minimum=1),visibility=enum("public","internal"),body=string(minLength=1,maxLength=3000)))
S["JobBody"]=obj(dict(expected_revision=integer(minimum=1),strategy=enum("workflow","agent"),expected_config_version=integer(minimum=1,description="Required in live mode: explicitly confirm the current model configuration before creating or retrying a potentially paid task.")),["strategy","expected_config_version"])
actionprops=dict(expected_revision=integer(minimum=1),draft_id=ID,apply_category=boolean(),apply_priority=boolean(),reason_code=enum("incorrect","unsupported","irrelevant","incomplete","other"),reply_text=string(maxLength=6000),expected_review_context=string(),acknowledgements=array(enum("no_evidence","sources_changed","context_omitted")))
S["DraftAction"]=obj(actionprops,["apply_category","apply_priority","reason_code","reply_text","expected_review_context","acknowledgements"])
S["ManualBody"]=obj(dict(expected_revision=integer(minimum=1),reply_text=string(minLength=1,maxLength=6000)))
S["ArticleCreate"]=obj(dict(title=string(minLength=1,maxLength=200),body=string(minLength=1,maxLength=20000),tags=array(string(minLength=1,maxLength=50),maxItems=10),expected_version=integer(),archived=boolean()),["expected_version","archived"])
S["ArticleBody"]=obj(dict(S["ArticleCreate"]["properties"],expected_version=integer(minimum=1)))
S["UserCreate"]=obj(dict(email=string(maxLength=254),display_name=string(minLength=1,maxLength=100),role=enum("admin","agent","requester"),password=string(minLength=12,maxLength=128)))
S["UserPatch"]=obj(dict(expected_revision=integer(minimum=1),display_name=string(minLength=1,maxLength=100),role=enum("admin","agent","requester"),disabled=boolean()))
S["SettingsBody"]=obj(dict(S["AIConfig"]["properties"],api_key=string(maxLength=4096),clear_api_key=boolean()),["api_key","clear_api_key"])
S["ApplyResult"]=obj(dict(ticket=ref("Ticket"),changed=boolean()))
S["CreatedID"]=obj(dict(id=ID))
S["ModelTest"]=obj(dict(basic_call_succeeded=boolean(),tools_verified=boolean(),model_call=ref("Call")))
S["Export"]=obj(dict(schema_version=integer(),exported_at=DATE,count=integer(),items=array(ref("Ticket"))))
ops=[
("get","/api/health",None,obj(dict(status=string(),version=string(),profile=enum("desktop","server"),workspace_mode=enum("demo","personal","server"),demo_accounts_available=boolean())),200),
("get","/api/ready",None,obj(dict(status=string())),200),
("post","/api/auth/login","LoginBody",ref("Me"),200),
("post","/api/auth/logout","Empty",None,204),
("get","/api/auth/me",None,ref("Me"),200),
("post","/api/auth/change-password","PasswordBody",None,204),
("get","/api/dashboard",None,ref("Dashboard"),200),
("get","/api/tickets",None,ref("TicketPage"),200),
("post","/api/tickets","TicketCreate",ref("Ticket"),201),
("get","/api/tickets/export",None,ref("Export"),200),
("get","/api/tickets/{id}",None,ref("TicketDetail"),200),
("patch","/api/tickets/{id}","TicketPatch",ref("Ticket"),200),
("post","/api/tickets/{id}/reopen","VersionBody",ref("Ticket"),200),
("get","/api/tickets/{id}/comments",None,array(ref("Comment")),200),
("post","/api/tickets/{id}/comments","CommentBody",ref("CreatedID"),201),
("get","/api/tickets/{id}/drafts",None,array(ref("Draft")),200),
("post","/api/tickets/{id}/jobs","JobBody",ref("Job"),202),
("post","/api/tickets/{id}/apply-suggestion","DraftAction",ref("ApplyResult"),200),
("post","/api/tickets/{id}/reject-draft","DraftAction",ref("Ticket"),200),
("post","/api/tickets/{id}/approve","DraftAction",ref("Ticket"),200),
("post","/api/tickets/{id}/reply","ManualBody",ref("Ticket"),200),
("get","/api/jobs",None,ref("JobPage"),200),
("get","/api/jobs/{id}",None,ref("Job"),200),
("post","/api/jobs/{id}/cancel","Empty",ref("Job"),200),
("post","/api/jobs/{id}/retry","JobBody",ref("Job"),202),
("post","/api/jobs/{id}/retry-save","Empty",ref("Job"),200),
("get","/api/knowledge",None,ref("ArticlePage"),200),
("post","/api/knowledge","ArticleCreate",ref("Article"),201),
("get","/api/knowledge/{id}",None,ref("Article"),200),
("patch","/api/knowledge/{id}","ArticleBody",ref("Article"),200),
("get","/api/users",None,array(ref("User")),200),
("post","/api/users","UserCreate",ref("User"),201),
("patch","/api/users/{id}","UserPatch",ref("User"),200),
("get","/api/settings/ai",None,ref("Settings"),200),
("put","/api/settings/ai","SettingsBody",ref("Settings"),200),
("post","/api/settings/ai/test","Empty",ref("ModelTest"),200),
("get","/api/audit",None,array(ref("Audit")),200)]
doc={"openapi":"3.0.3","info":{"title":"FlowDesk API","version":SOFTWARE_VERSION,"description":"Single-workspace support drafting with human review. Cookies and exact Origin/Host are required. No CORS. Requester sees own public resources. Demo never calls a provider. AI output is untrusted until reviewed."},"paths":{},"components":{"schemas":S,"securitySchemes":{"workspaceSession":{"type":"apiKey","in":"cookie","name":"flowdesk_session_<workspace_id>","description":"Server uses __Host-flowdesk_<workspace_id>, HttpOnly, Secure, SameSite=Strict. Desktop cookie is scoped by stable workspace ID."}}}}
for method,path,body,data,status in ops:
    v={"operationId":method+"_"+path.replace("/api/","").replace("/","_").replace("{id}","by_id").replace("-","_"),"security":[] if path in ("/api/health","/api/ready","/api/auth/login") else [{"workspaceSession":[]}],"responses":{}}
    v["responses"][str(status)]={"description":"Success" if status!=204 else "No content"}
    if data is not None:
        env=obj(dict(data=data,request_id=string(),replayed=boolean()),["replayed"])
        v["responses"][str(status)]["content"]={"application/json":{"schema":env}}
    for code in (400,401,403,404,405,409,429,500,503):v["responses"][str(code)]={"description":"Safe error with guide code","content":{"application/json":{"schema":ref("ErrorEnvelope")}}}
    v["parameters"]=[{"name":"id","in":"path","required":True,"schema":ID}] if "{id}" in path else []
    if body:
        v["requestBody"]={"required":True,"content":{"application/json":{"schema":ref(body)}}}
        v["parameters"].append({"name":"Origin","in":"header","required":True,"schema":string(),"description":"Exact configured public origin, including scheme and port."})
        if path not in ("/api/auth/login","/api/auth/logout","/api/auth/change-password","/api/settings/ai/test","/api/jobs/{id}/retry-save"):v["parameters"].append({"name":"Idempotency-Key","in":"header","required":False,"schema":string(maxLength=128),"description":"Same key and canonical typed payload replay for 24 hours, scoped to actor/method/concrete path. Reauthorization precedes replay."})
    query=[]
    if method=="get" and path in ("/api/tickets","/api/tickets/export"):
        query=[("status",STATUS),("category",CATEGORY),("priority",PRIORITY),("search",string(maxLength=200))]
    if path in ("/api/tickets","/api/jobs","/api/knowledge") and method=="get":query += [("page",integer(minimum=1,maximum=100000)),("page_size",integer(minimum=1,maximum=100))]
    if path=="/api/jobs" and method=="get":query += [("ticket_id",ID)]
    if path=="/api/knowledge" and method=="get":query += [("include_archived",boolean()),("search",string(maxLength=200))]
    if path=="/api/audit":query += [("ticket_id",ID)]
    if path=="/api/tickets/export":
        query += [("format",enum("json","csv"))]
        v["responses"]["200"]["content"]["application/json"]["schema"]=ref("Export")
        v["responses"]["200"]["content"]["text/csv"]={"schema":string()}
    v["parameters"] += [{"name":k,"in":"query","required":path=="/api/audit" or k=="format","schema":s} for k,s in query]
    doc["paths"].setdefault(path,{})[method]=v
(ROOT/"api").mkdir(exist_ok=True)
(ROOT/"api"/"openapi.json").write_text(json.dumps(doc,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
print("OpenAPI operations:",len(ops))


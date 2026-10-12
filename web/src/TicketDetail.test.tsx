import {render,screen,waitFor,within} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {describe,it,expect,vi,afterEach} from 'vitest';
import {TicketDetail} from './TicketDetail';
import {WorkspaceProvider,useWorkspace} from './context';
import {editVault} from './vault';
import type {Me,Ticket,Draft,Dashboard,Job} from './types';
const me:Me={id:'1',email:'owner@example.test',display_name:'Owner',role:'admin',disabled:false,revision:1,authorization_version:1,workspace_id:'space1',profile:'desktop'};
const t:Ticket={id:'1',display_id:'FD-0001',title:'API 401',description:'Authentication fails',requester_id:'3',requester_name:'Requester',assignee_id:'2',assignee_name:'Agent',category:'other',priority:'normal',status:'awaiting_review',revision:3,content_version:1,conversation_version:1,cycle_no:1,approved_reply_id:null,created_at:'2026-10-11T00:00:00Z',updated_at:'2026-10-11T00:00:00Z',resolved_at:null};
const d=(id:string,summary:string):Draft=>({id,job_id:id,ticket_id:'1',content_version:1,conversation_version:1,cycle_no:1,provider_mode:'demo',summary,suggested_category:'technical',suggested_priority:'normal',reply_text:'Generated '+id,evidence_ids:[],needs_more_information:true,context_omitted:false,warning_codes:['no_evidence'],sources:[],review_state:'pending',review_context:{token:'token-1',heads:[],required_acknowledgements:['no_evidence']},valid:true,created_at:'2026-10-11T00:00:00Z'});
const dashboard:Dashboard={total:1,open:0,in_progress:0,awaiting_review:1,resolved:0,generated:2,demo_generated:2,live_generated:0,approved:0,rejected:0,manual:0,assignees:[{id:'2',display_name:'Agent'}],ai_mode:'demo',ai_strategy:'workflow',ai_config_version:1,storage_blocked:false};
const job=(status:Job['status']):Job=>({id:'101',ticket_id:'1',actor_id:'1',parent_job_id:null,provider_mode:'demo',execution_strategy:'workflow',configuration:{mode:'demo',strategy:'workflow',base_url:'',model:'',timeout_seconds:60,output_token_limit:2048,pricing:null,config_version:1},status,error_code:null,error_stage:null,request_id:'race-request',created_at:'2026-10-11T00:00:00Z',started_at:null,finished_at:null,steps:[],model_calls:[]});
afterEach(()=>{vi.unstubAllGlobals();editVault.clear()});
function Refresh(){const {reload}=useWorkspace();return <button onClick={reload}>刷新测试</button>}
function fixture(publishFails=false){let drafts=[d('2','Latest result'),d('1','Older result')];let token='token-1';const posts:{path:string;body:Record<string,unknown>}[]=[];vi.stubGlobal('fetch',vi.fn(async(p:string,o:RequestInit)=>{if(o.method==='POST'){posts.push({path:p,body:JSON.parse(o.body as string)});if(p.endsWith('/approve')&&publishFails)return new Response(JSON.stringify({error:{code:'VERSION_CONFLICT'},request_id:'publish-failed'}),{status:409});if(p.endsWith('/jobs'))drafts=[d('3','New result'),...drafts];return new Response(JSON.stringify({data:{id:'99'}}),{status:202})};let value:unknown;const current=drafts.map(x=>({...x,review_context:{...x.review_context,token}}));if(p==='/api/dashboard')value=dashboard;else if(p==='/api/tickets/1')value={ticket:t,comments:[],replies:[],work:{drafts:current,jobs:[]}};else if(p.endsWith('/drafts'))value=current;else if(p.includes('/api/jobs?'))value={items:[],page:1,page_size:10,total:0};else value=[];return new Response(JSON.stringify({data:value}),{status:200})}));editVault.bind(me);render(<WorkspaceProvider me={me} onLogout={()=>{}}><Refresh/><TicketDetail id="1"/></WorkspaceProvider>);return {posts,setToken:(v:string)=>{token=v}}}
describe('ticket review interactions',()=>{
 it('adds new result above old cards and preserves a selected manual edit',async()=>{const f=fixture();await screen.findByText('Latest result');const u=userEvent.setup();const box=await screen.findByLabelText('回复草稿 · 可编辑');await u.clear(box);await u.type(box,'Human edited reply');await u.click(screen.getByRole('button',{name:'生成建议'}));await screen.findByText('New result');const titles=screen.getAllByRole('heading',{level:3}).map(h=>h.textContent);expect(titles).toEqual(['New result','Latest result','Older result']);expect(screen.getByLabelText('回复草稿 · 可编辑')).toHaveValue('Human edited reply');expect(f.posts[0].path).toBe('/api/tickets/1/jobs');expect(f.posts[0].body.expected_config_version).toBe(1)});
 it('requires acknowledgements, binds them to review token and keeps edited text',async()=>{const f=fixture();await screen.findByText('Latest result');const u=userEvent.setup();expect(screen.getByRole('button',{name:'核对并发布'})).toBeDisabled();await u.click(screen.getByLabelText('当前没有检索来源，我已自行核实回复中的事实'));expect(screen.getByRole('button',{name:'核对并发布'})).toBeEnabled();const box=screen.getByLabelText('回复草稿 · 可编辑');await u.clear(box);await u.type(box,'Checked human text');f.setToken('token-2');await u.click(screen.getByText('刷新测试'));await waitFor(()=>expect(screen.getByRole('button',{name:'核对并发布'})).toBeDisabled());expect(screen.getByLabelText('回复草稿 · 可编辑')).toHaveValue('Checked human text');await u.click(screen.getByLabelText('当前没有检索来源，我已自行核实回复中的事实'));await u.click(screen.getByRole('button',{name:'核对并发布'}));const modal=screen.getByRole('dialog');expect(within(modal).getByText('Checked human text')).toBeInTheDocument();await u.click(within(modal).getByRole('button',{name:'确认发布'}));expect(f.posts.at(-1)?.body).toMatchObject({draft_id:'2',reply_text:'Checked human text',expected_review_context:'token-2',acknowledgements:['no_evidence']})});
});

describe('completed operation feedback',()=>{
 it('announces publication immediately after the server confirms success',async()=>{
  fixture();const u=userEvent.setup();await screen.findByText('Latest result');
  await u.click(screen.getByLabelText('当前没有检索来源，我已自行核实回复中的事实'));
  await u.click(screen.getByRole('button',{name:'核对并发布'}));
  await u.click(within(screen.getByRole('dialog')).getByRole('button',{name:'确认发布'}));
  expect(await screen.findByText('已核对并发布，申请人现在可以查看正式回复。')).toBeInTheDocument();
 });
 it('keeps the confirmation and never announces success when publishing fails',async()=>{
  fixture(true);const u=userEvent.setup();await screen.findByText('Latest result');
  await u.click(screen.getByLabelText('当前没有检索来源，我已自行核实回复中的事实'));
  await u.click(screen.getByRole('button',{name:'核对并发布'}));
  await u.click(within(screen.getByRole('dialog')).getByRole('button',{name:'确认发布'}));
  await waitFor(()=>expect(within(screen.getByRole('dialog')).getByRole('button',{name:'确认发布'})).toBeEnabled());
  expect(screen.queryByText('已核对并发布，申请人现在可以查看正式回复。')).not.toBeInTheDocument();
 });
 it.each(['admin','agent'] as const)('refreshes a finished job and its draft together for %s despite older separate responses',async role=>{
  let reads=0;const currentMe={...me,role};
  const fetcher=vi.fn(async(p:string)=>{
   let value:unknown;
   if(p==='/api/tickets/1'){
    const first=++reads===1;
    value={ticket:first?{...t,status:'in_progress',active_job_id:'101'}:t,comments:[],replies:[],work:{drafts:first?[]:[d('101','Completed result')],jobs:[job(first?'running':'succeeded')]}};
   }else if(p==='/api/dashboard')value=dashboard;
   else if(p.endsWith('/drafts'))value=[];
   else if(p.startsWith('/api/jobs?'))value={items:[job('succeeded')],total:1,page:1,page_size:10};
   else value=[];
   return new Response(JSON.stringify({data:value}));
  });
  vi.stubGlobal('fetch',fetcher);editVault.bind(currentMe);
  render(<WorkspaceProvider me={currentMe} onLogout={()=>{}}><TicketDetail id="1"/></WorkspaceProvider>);
  expect(await screen.findByText('Completed result',{}, {timeout:3400})).toBeInTheDocument();
  expect(screen.queryByRole('button',{name:'取消任务'})).not.toBeInTheDocument();
  expect(screen.queryByText('任务独立在后台执行。离开页面不会取消任务。')).not.toBeInTheDocument();
  expect(reads).toBeGreaterThan(1);
 });
});


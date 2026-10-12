import {useState} from 'react';
import {render,screen,waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {describe,it,expect,vi,afterEach} from 'vitest';
import {WorkspaceProvider,useWorkspace,useAction} from './context';
import {Notices} from './Notices';
import {ApiError,guides} from './api';
import type {Me} from './types';
const me:Me={id:'1',email:'owner@example.test',display_name:'Owner',role:'admin',disabled:false,revision:1,authorization_version:1,workspace_id:'space1',profile:'desktop'};
afterEach(()=>{vi.unstubAllGlobals();sessionStorage.clear()});
function Frame(){const {notice}=useWorkspace();const [page,setPage]=useState('A');return <><button onClick={()=>notice(new ApiError('PROVIDER_AUTH','r-auth','model_request_2'),'job-1')}>认证失败</button><button onClick={()=>setPage('B')}>切换页面</button><p>页面 {page}</p><Notices center={false} onCenterClose={()=>{}}/></>}
describe('workspace feedback',()=>{
 it('keeps failure across pages, shows guidance and closes with X',async()=>{render(<WorkspaceProvider me={me} onLogout={()=>{}}><Frame/></WorkspaceProvider>);const u=userEvent.setup();await u.click(screen.getByText('认证失败'));await u.click(screen.getByText('切换页面'));expect(screen.getByText('页面 B')).toBeInTheDocument();expect(screen.getByText(guides.PROVIDER_AUTH.title)).toBeInTheDocument();await u.click(screen.getByRole('button',{name:'详细引导'}));expect(screen.getByRole('dialog')).toBeInTheDocument();expect(screen.getByText(guides.PROVIDER_AUTH.steps[0])).toBeInTheDocument();await u.click(screen.getByRole('button',{name:'关闭对话框'}));await u.click(screen.getByLabelText('关闭提醒：'+guides.PROVIDER_AUTH.title));expect(screen.queryByRole('alert')).not.toBeInTheDocument();await u.click(screen.getByText('认证失败'));expect(screen.queryByRole('alert')).not.toBeInTheDocument()});
 it('manual retry keeps original key and prevents a double submit',async()=>{const keys:string[]=[];let finish!:(r:Response)=>void;let count=0;vi.stubGlobal('fetch',vi.fn((_p:string,o:RequestInit)=>{keys.push((o.headers as Record<string,string>)['Idempotency-Key']);count++;if(count===1)return Promise.reject(new TypeError('offline'));return new Promise<Response>(r=>{finish=r})}));function Action(){const {run,busy}=useAction();return <button disabled={busy} onClick={()=>void run('/api/tickets','POST',{title:'same'})}>提交</button>};render(<WorkspaceProvider me={me} onLogout={()=>{}}><Action/></WorkspaceProvider>);const u=userEvent.setup();await u.click(screen.getByText('提交'));await waitFor(()=>expect(screen.getByText('提交')).toBeEnabled());expect(keys).toHaveLength(1);await u.click(screen.getByText('提交'));await u.click(screen.getByText('提交'));expect(keys).toHaveLength(2);expect(keys[0]).toBe(keys[1]);finish(new Response(JSON.stringify({data:{ok:true}}),{status:201}));await waitFor(()=>expect(screen.getByText('提交')).toBeEnabled())});
});


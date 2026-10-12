import {render,screen} from '@testing-library/react';
import {it,expect,vi,afterEach} from 'vitest';
import {WorkspaceProvider} from './context';
import {Jobs} from './Jobs';
import type {Me} from './types';
const me:Me={id:'1',email:'demo@example.test',display_name:'Demo',role:'admin',disabled:false,revision:1,authorization_version:1,workspace_id:'jobs-isolated',profile:'desktop'};
afterEach(()=>{vi.unstubAllGlobals();sessionStorage.clear()});
it('cannot retry before the current model mode and fee-consent version load',async()=>{
 const fetcher=vi.fn((path:string)=>{
  if(path==='/api/dashboard')return new Promise<Response>(()=>{});
  return Promise.resolve(new Response(JSON.stringify({data:{items:[{id:'1',ticket_id:'1',status:'failed',provider_mode:'demo',execution_strategy:'workflow',created_at:'2026-10-11T00:00:00Z',model_calls:[],steps:[],error_code:'OUTPUT_INVALID',error_stage:'output_validation',request_id:'request-1',runtime_hint:null}],total:1,page:1,page_size:20}})));
 });
 vi.stubGlobal('fetch',fetcher);
 render(<WorkspaceProvider me={me} onLogout={()=>{}}><Jobs/></WorkspaceProvider>);
 const retry=await screen.findByRole('button',{name:'重新生成'});
 expect(retry).toBeDisabled();
 expect(fetcher.mock.calls.every(x=>x[0]==='/api/dashboard'||x[0].startsWith('/api/jobs?'))).toBe(true);
 expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});


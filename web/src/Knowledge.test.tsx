import {render,screen,waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {it,expect,vi,afterEach} from 'vitest';
import {WorkspaceProvider} from './context';
import {Knowledge} from './Knowledge';
import type {Me} from './types';
const me:Me={id:'1',email:'owner@example.test',display_name:'Owner',role:'admin',disabled:false,revision:1,authorization_version:1,workspace_id:'knowledge-test',profile:'desktop'};
afterEach(()=>vi.unstubAllGlobals());
it('imports editable text locally and only persists it after explicit save',async()=>{
 const writes:Record<string,unknown>[]=[];
 vi.stubGlobal('fetch',vi.fn(async(_p:string,o:RequestInit)=>{
  if(o.method==='POST'){writes.push(JSON.parse(o.body as string));return new Response(JSON.stringify({data:{id:'1'}}),{status:201})}
  return new Response(JSON.stringify({data:{items:[],page:1,page_size:20,total:0}}));
 }));
 render(<WorkspaceProvider me={me} onLogout={()=>{}}><Knowledge/></WorkspaceProvider>);
 const u=userEvent.setup();await u.click(screen.getByRole('button',{name:'新增资料'}));
 const content='# 安装说明\n重启后重新连接。';const bytes=new TextEncoder().encode(content);
 const file=new File([bytes],'安装说明.md',{type:'text/markdown'});
 Object.defineProperty(file,'arrayBuffer',{value:async()=>bytes.buffer});
 await u.upload(screen.getByLabelText(/^从 TXT \/ Markdown 导入/),file);
 await waitFor(()=>expect(screen.getByLabelText(/^正文/)).toHaveValue(content));
 expect(screen.getByLabelText('资料标题')).toHaveValue('安装说明');expect(writes).toHaveLength(0);
 await u.click(screen.getByRole('button',{name:'保存资料'}));
 await waitFor(()=>expect(writes).toHaveLength(1));
 expect(writes[0]).toMatchObject({title:'安装说明',body:content});
});

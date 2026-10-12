import {render,screen,waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {it,expect,vi,afterEach} from 'vitest';
import {Login} from './Login';
afterEach(()=>vi.unstubAllGlobals());
it('does not display public demo credentials in a personal workspace',async()=>{
 vi.stubGlobal('fetch',vi.fn(async()=>new Response(JSON.stringify({data:{profile:'desktop',workspace_mode:'personal',demo_accounts_available:false}}))));
 render(<Login expired={false} onLogin={()=>{}}/>);
 await screen.findByText('本机工作台');
 expect(screen.queryByText('快速体验')).not.toBeInTheDocument();
 expect(screen.queryByText(/FlowDeskDemo/)).not.toBeInTheDocument();
 expect(screen.getByLabelText('邮箱')).toHaveValue('');
});
it('retains explicit quick login for the separate demo workspace',async()=>{
 vi.stubGlobal('fetch',vi.fn(async()=>new Response(JSON.stringify({data:{profile:'desktop',workspace_mode:'demo',demo_accounts_available:true}}))));
 render(<Login expired={false} onLogin={()=>{}}/>);
 const role=await screen.findByRole('button',{name:'管理员'});
 await userEvent.setup().click(role);
 await waitFor(()=>expect(screen.getByLabelText('邮箱')).toHaveValue('admin@flowdesk.demo'));
 expect(screen.getByLabelText('密码')).toHaveValue('FlowDeskDemo!2026');
});

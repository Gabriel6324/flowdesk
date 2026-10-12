import {useEffect} from 'react';
import {render,screen} from '@testing-library/react';
import {describe,it,expect,vi,afterEach} from 'vitest';
import {api,bumpSession,download,ApiError} from './api';
import {WorkspaceProvider,useWorkspace} from './context';
import {Notices} from './Notices';
import type {Me} from './types';
const me:Me={id:'9',email:'demo@example.test',display_name:'Demo',role:'admin',disabled:false,revision:1,authorization_version:1,workspace_id:'isolated',profile:'desktop'};
afterEach(()=>{vi.unstubAllGlobals();sessionStorage.clear()});
describe('session boundaries',()=>{
 it('rejects old successful data after a new login',async()=>{let resolve!:(r:Response)=>void;vi.stubGlobal('fetch',vi.fn(()=>new Promise<Response>(r=>{resolve=r})));const pending=api('/api/tickets/1');bumpSession();resolve(new Response(JSON.stringify({data:{description:'old private data'}})));await expect(pending).rejects.toMatchObject({name:'AbortError'})});
 it('does not download an old successful export after switching users',async()=>{let resolve!:(r:Response)=>void;vi.stubGlobal('fetch',vi.fn(()=>new Promise<Response>(r=>{resolve=r})));const pending=download('/api/tickets/export?format=csv','old.csv');bumpSession();resolve(new Response('old account records'));await expect(pending).rejects.toMatchObject({name:'AbortError'})});
 it('loads closed reminder identities before child effects can republish',()=>{sessionStorage.setItem('flowdesk.dismissed.isolated.9',JSON.stringify(['job-old']));function Publish(){const {notice}=useWorkspace();useEffect(()=>notice(new ApiError('PROVIDER_AUTH'),'job-old'),[notice]);return <Notices center={false} onCenterClose={()=>{}}/>};render(<WorkspaceProvider me={me} onLogout={()=>{}}><Publish/></WorkspaceProvider>);expect(screen.queryByRole('alert')).not.toBeInTheDocument()});
});


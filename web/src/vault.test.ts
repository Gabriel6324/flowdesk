import {describe,it,expect,vi,afterEach} from 'vitest';
import {EditVault} from './vault';
import type {Me} from './types';
const me:Me={id:'1',email:'owner@example.test',display_name:'Owner',role:'admin',disabled:false,revision:1,authorization_version:1,workspace_id:'space1',profile:'desktop'};
afterEach(()=>vi.restoreAllMocks());
describe('manual edit isolation',()=>{
 it('requires explicit restore after same account login',()=>{const v=new EditVault();v.bind(me);v.put('draft','human edit');v.expire();expect(v.get('draft')).toBeUndefined();expect(v.bind(me)).toBe(true);v.recover();expect(v.get('draft')).toBe('human edit')});
 it('discards edits for other user, workspace or authorization epoch',()=>{for(const change of [{id:'2'},{workspace_id:'space2'},{authorization_version:2}]){const v=new EditVault();v.bind(me);v.put('x','private edit');v.expire();expect(v.bind({...me,...change})).toBe(false);v.recover();expect(v.get('x')).toBeUndefined()}});
 it('does not keep expired or unlimited edit buffers',()=>{const now=Date.now();vi.spyOn(Date,'now').mockReturnValue(now);const v=new EditVault();v.bind(me);for(let i=0;i<11;i++)v.put(String(i),'x');expect(v.get('0')).toBeUndefined();expect(v.get('10')).toBe('x');vi.spyOn(Date,'now').mockReturnValue(now+600001);expect(v.get('10')).toBeUndefined()});
 it('counts unicode characters and limits total content',()=>{const v=new EditVault();v.bind(me);for(let i=0;i<11;i++)v.put(String(i),'好'.repeat(6000));expect(v.get('0')).toBeUndefined();expect(v.get('10')?.length).toBe(6000)});
});


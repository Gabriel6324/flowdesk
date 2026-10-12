import {useState} from 'react';
import {render,screen,fireEvent} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {describe,it,expect,vi} from 'vitest';
import {Button,Modal} from './ui';
describe('controls',()=>{
 it('disabled action gives its specific reason and cannot execute',async()=>{const click=vi.fn();render(<Button reason="先结束活动任务" onClick={click}>发布</Button>);expect(screen.getByRole('button',{name:'发布'})).toBeDisabled();expect(screen.getByLabelText('先结束活动任务')).toHaveAttribute('title','先结束活动任务');await userEvent.click(screen.getByRole('button',{name:'发布'}));expect(click).not.toHaveBeenCalled()});
 it('modal keeps input focus on rerender and handles Escape',async()=>{function Test(){const [open,setOpen]=useState(false);const [text,setText]=useState('');return <><button onClick={()=>setOpen(true)}>打开</button>{open&&<Modal title="编辑" onClose={()=>setOpen(false)}><label>正文<input value={text} onChange={e=>setText(e.target.value)}/></label></Modal>}</>};render(<Test/>);const user=userEvent.setup();await user.click(screen.getByText('打开'));const input=screen.getByLabelText('正文');await user.click(input);await user.type(input,'draft');expect(input).toHaveFocus();expect(input).toHaveValue('draft');fireEvent.keyDown(document,{key:'Escape'});expect(screen.queryByRole('dialog')).not.toBeInTheDocument();expect(screen.getByText('打开')).toHaveFocus()});
});


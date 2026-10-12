import {render,screen} from '@testing-library/react';
import {it,expect} from 'vitest';
import {WorkspaceSetup} from './WorkspaceSetup';
it('links an empty workspace to real setup steps without claiming a model is connected',()=>{
 render(<WorkspaceSetup setup={{knowledge_articles:0,requester_accounts:0,staff_accounts:1,model_configured:false}}/>);
 expect(screen.getByRole('link',{name:'配置与测试'})).toHaveAttribute('href','#/settings');
 expect(screen.getByRole('link',{name:'录入资料'})).toHaveAttribute('href','#/knowledge');
 expect(screen.getByRole('link',{name:'管理账号'})).toHaveAttribute('href','#/users');
 expect(screen.getByText('尚未配置真实接口；当前离线规则不代表模型效果')).toBeInTheDocument();
});
it('describes saved model settings as configured while still requiring an actual test',()=>{
 render(<WorkspaceSetup setup={{knowledge_articles:2,requester_accounts:1,staff_accounts:2,model_configured:true}}/>);
 expect(screen.getByText('真实接口已保存，请测试连接并核对实际生成结果')).toBeInTheDocument();
});

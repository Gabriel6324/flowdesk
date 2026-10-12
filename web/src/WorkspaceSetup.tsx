import {CheckCircle2,ArrowUpRight,Circle} from 'lucide-react';
import type {Schemas} from './types';
export function WorkspaceSetup({setup:s}:{setup:Schemas['WorkspaceReadiness']}){
 const steps=[
  {title:'人员账号',ready:s.staff_accounts>0&&s.requester_accounts>0,detail:s.requester_accounts>0?`${s.staff_accounts} 位处理人员 · ${s.requester_accounts} 位申请人`:'管理员可处理工单；按需要添加工作人员和申请人',href:'#/users',action:'管理账号'},
  {title:'公开资料',ready:s.knowledge_articles>0,detail:s.knowledge_articles>0?`${s.knowledge_articles} 篇活跃资料`:'先录入允许用于回复的产品说明或常见问题',href:'#/knowledge',action:'录入资料'},
  {title:'真实模型',ready:s.model_configured,detail:s.model_configured?'真实接口已保存，请测试连接并核对实际生成结果':'尚未配置真实接口；当前离线规则不代表模型效果',href:'#/settings',action:'配置与测试'},
 ];
 return <section className="card workspace-setup" aria-label="工作台使用准备"><div className="section-heading"><div><p className="eyebrow">GET READY</p><h2>用自己的资料和工单开始</h2><p className="muted small">准备情况来自当前工作台；连接测试成功后，再生成一条草稿确认完整流程。</p></div><a className="button secondary" href="#/tickets?new=1">提交工单<ArrowUpRight size={15}/></a></div><div className="setup-steps">{steps.map(x=><div key={x.title} className={'setup-step '+(x.ready?'ready':'')}><div>{x.ready?<CheckCircle2 size={19}/>:<Circle size={19}/>}<b>{x.title}</b></div><p>{x.detail}</p><a className="text-button" href={x.href}>{x.action}<ArrowUpRight size={14}/></a></div>)}</div><p className="small muted">未接入模型时也可人工处理；配置模型不会自动发布回复。</p></section>
}

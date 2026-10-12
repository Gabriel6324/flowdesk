import type { components } from './generated/api';
export type Schemas=components['schemas'];
export type Me=Schemas['Me'];
export type Ticket=Schemas['Ticket'];
export type TicketDetail=Schemas['TicketDetail'];
export type Draft=Schemas['Draft'];
export type Job=Schemas['Job'];
export type Article=Schemas['Article'];
export type User=Schemas['User'];
export type Settings=Schemas['Settings'];
export type Dashboard=Schemas['Dashboard'];
export type Audit=Schemas['Audit'];
export type Page<T>={items:T[];page:number;page_size:number;total:number};
export const categoryLabels={account:'账号',billing:'账单',technical:'技术',other:'其他'} as const;
export const priorityLabels={low:'低',normal:'普通',high:'高',urgent:'紧急'} as const;
export const statusLabels={open:'待受理',in_progress:'处理中',awaiting_review:'待审核',resolved:'已解决',queued:'排队中',running:'执行中',succeeded:'已生成',failed:'失败',canceled:'已取消',stale:'已过期'} as const;
export const roleLabels={admin:'管理员',agent:'处理员',requester:'申请人'} as const;
export const strategyLabels={workflow:'Workflow · 固定流程',agent:'Agent · 只读工具'} as const;
export const chars=(s:string)=>Array.from(s).length;
export const date=(s?:string|null)=>s?new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}).format(new Date(s)):'—';
export function label(s:string){return (statusLabels as Record<string,string>)[s]??s}


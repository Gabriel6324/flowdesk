import type {Me} from './types';
type Owner={workspace:string;user:string;authorization:number};
type Entry={text:string;at:number};
export class EditVault {
 private owner:Owner|null=null;private entries=new Map<string,Entry>();private quarantined=false;
 bind(me:Me){const next={workspace:me.workspace_id,user:me.id,authorization:me.authorization_version};if(!this.owner||JSON.stringify(this.owner)!==JSON.stringify(next)){this.clear();this.owner=next}this.clean();return this.quarantined&&this.entries.size>0}
 private clean(){for(const[k,v]of this.entries)if(Date.now()-v.at>600000)this.entries.delete(k)}
 put(key:string,text:string){if(this.quarantined)return;this.clean();this.entries.delete(key);this.entries.set(key,{text,at:Date.now()});while(this.entries.size>10||[...this.entries.values()].reduce((n,v)=>n+Array.from(v.text).length,0)>60000){const oldest=this.entries.keys().next().value;if(oldest===undefined)break;this.entries.delete(oldest)}}
 get(key:string){this.clean();return this.quarantined?undefined:this.entries.get(key)?.text}
 expire(){this.quarantined=true;this.clean()}
 recover(){this.quarantined=false;this.clean()}
 clear(){this.entries.clear();this.owner=null;this.quarantined=false}
}
export const editVault=new EditVault();


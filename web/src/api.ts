import guides from '../../internal/domain/guides.json';
export {guides};
export type ErrorCode=keyof typeof guides;
export class ApiError extends Error {
  constructor(public code:ErrorCode,public requestId='',public stage='',public status=0){super(guides[code]?.title??guides.UNKNOWN_ERROR.title)}
}
export function asError(e:unknown):ApiError{return e instanceof ApiError?e:new ApiError('UNKNOWN_ERROR')}
export const newKey=()=>crypto.randomUUID();
let generation=0;
export const bumpSession=()=>{generation++};
type Options={method?:string;body?:unknown;key?:string;signal?:AbortSignal;timeoutMs?:number};
export async function api<T>(path:string,o:Options={}):Promise<T>{
  const startedGeneration=generation;
  const c=new AbortController();let timedOut=false;const timer=setTimeout(()=>{timedOut=true;c.abort()},o.timeoutMs??15000);
  const abort=()=>c.abort();o.signal?.addEventListener('abort',abort,{once:true});if(o.signal?.aborted)c.abort();
  try {
    const r=await fetch(path,{method:o.method??'GET',credentials:'same-origin',cache:'no-store',headers:{...(o.body!==undefined?{'Content-Type':'application/json'}:{}),...(o.key?{'Idempotency-Key':o.key}:{})},body:o.body===undefined?undefined:JSON.stringify(o.body),signal:c.signal});
    if(!r.ok){let value:unknown;try{value=await r.json()}catch{};const v=value as {error?:{code?:string;stage?:string};request_id?:string}|undefined;
      const code=v?.error?.code&&v.error.code in guides?v.error.code as ErrorCode:'UNKNOWN_ERROR';
      if((r.status===401)&&path!='/api/auth/login'&&startedGeneration===generation)window.dispatchEvent(new CustomEvent('flowdesk:session-expired'));
      throw new ApiError(code,v?.request_id??r.headers.get('X-Request-ID')??'',v?.error?.stage??'',r.status);
    }
    if(startedGeneration!==generation)throw new DOMException('Session changed','AbortError');if(r.status===204)return undefined as T;
    const v=await r.json() as {data:T};if(startedGeneration!==generation)throw new DOMException('Session changed','AbortError');if(!Object.prototype.hasOwnProperty.call(v,'data'))throw new ApiError('UNKNOWN_ERROR');return v.data;
  }catch(e){if(e instanceof ApiError)throw e;if(o.signal?.aborted||startedGeneration!==generation)throw new DOMException('Aborted','AbortError');throw new ApiError(timedOut?'CLIENT_REQUEST_TIMEOUT':'CLIENT_NETWORK')}
  finally{clearTimeout(timer);o.signal?.removeEventListener('abort',abort)}
}
export async function download(path:string,filename:string){const startedGeneration=generation;let r:Response;try{r=await fetch(path,{credentials:'same-origin',cache:'no-store',signal:AbortSignal.timeout(15000)})}catch(e){throw new ApiError(e instanceof DOMException&&e.name==='TimeoutError'?'CLIENT_REQUEST_TIMEOUT':'CLIENT_NETWORK')};if(!r.ok){let v:{error?:{code?:string};request_id?:string}={};try{v=await r.json()}catch{};if(r.status===401&&startedGeneration===generation)window.dispatchEvent(new CustomEvent('flowdesk:session-expired'));throw new ApiError(v.error?.code&&v.error.code in guides?v.error.code as ErrorCode:'UNKNOWN_ERROR',v.request_id??'', 'export',r.status)};const blob=await r.blob();if(startedGeneration!==generation)throw new DOMException('Session changed','AbortError');const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=filename;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}


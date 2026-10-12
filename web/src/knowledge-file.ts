import {ApiError} from './api';
export async function readKnowledgeFile(file:Pick<File,'name'|'size'|'arrayBuffer'>){
 if(!/\.(txt|md|markdown)$/i.test(file.name))throw new ApiError('KNOWLEDGE_IMPORT_FORMAT','','knowledge_import');
 if(file.size>80000)throw new ApiError('KNOWLEDGE_IMPORT_TOO_LARGE','','knowledge_import');
 let body:string;
 try{body=new TextDecoder('utf-8',{fatal:true}).decode(await file.arrayBuffer())}catch{throw new ApiError('KNOWLEDGE_IMPORT_FORMAT','','knowledge_import')}
 if(!body.trim()||body.includes('\0'))throw new ApiError('KNOWLEDGE_IMPORT_FORMAT','','knowledge_import');
 if(Array.from(body).length>20000)throw new ApiError('KNOWLEDGE_IMPORT_TOO_LARGE','','knowledge_import');
 const title=Array.from(file.name.replace(/\.(txt|md|markdown)$/i,'')).slice(0,200).join('');
 return {title,body};
}

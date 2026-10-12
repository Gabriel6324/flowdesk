import {describe,it,expect} from 'vitest';
import {readKnowledgeFile} from './knowledge-file';
function file(name:string,content:string|Uint8Array){
 const bytes=typeof content==='string'?new TextEncoder().encode(content):content;
 return {name,size:bytes.byteLength,arrayBuffer:async()=>bytes.buffer as ArrayBuffer};
}
describe('public knowledge file import',()=>{
 it('retains Chinese text, line breaks and Markdown as editable text',async()=>{
  const body='# 登录说明\n\n请先核对邮箱。\n';
  expect(await readKnowledgeFile(file('登录说明.md',body))).toEqual({title:'登录说明',body});
 });
 it('rejects invalid UTF-8 and binary or empty input without silently altering it',async()=>{
  for(const input of [file('bad.txt',new Uint8Array([0xff])),file('binary.txt','one\0two'),file('empty.txt','  '),file('notes.pdf','text')]){
   await expect(readKnowledgeFile(input)).rejects.toMatchObject({code:'KNOWLEDGE_IMPORT_FORMAT'});
  }
 });
 it('enforces both byte and Unicode character limits, including multi-byte input',async()=>{
  await expect(readKnowledgeFile(file('long.txt','a'.repeat(20001)))).rejects.toMatchObject({code:'KNOWLEDGE_IMPORT_TOO_LARGE'});
  await expect(readKnowledgeFile(file('many.txt','😀'.repeat(20001)))).rejects.toMatchObject({code:'KNOWLEDGE_IMPORT_TOO_LARGE'});
  expect((await readKnowledgeFile(file('within.txt','😀'.repeat(20000)))).body).toHaveLength(40000);
 });
});

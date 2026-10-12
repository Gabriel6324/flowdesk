#!/usr/bin/env node
// Real browser checks in a new fictional personal workspace, never user data.
const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const {spawn,execFileSync}=require('node:child_process');
const assert=require('node:assert/strict');
const opts={};for(let i=2;i<process.argv.length;i+=2)opts[process.argv[i].replace(/^--/,'')]=process.argv[i+1];
if(!opts.binary||!opts.out)throw Error('Require --binary and a new --out');
const binary=path.resolve(opts.binary),out=path.resolve(opts.out);if(fs.existsSync(out))throw Error('Output must be new');
const pw=require(opts['playwright-module']??'playwright');const checks=[];const mark=check=>checks.push({check,status:'passed'});
const password='BrowserStrong!2026';let child,browser;
async function port(){return new Promise(resolve=>{const server=net.createServer();server.listen(0,'127.0.0.1',()=>{const p=server.address().port;server.close(()=>resolve(p))})})}
async function login(page,email){await page.goto(base);await page.getByLabel('邮箱',{exact:true}).fill(email);await page.getByLabel('密码',{exact:true}).fill(password);await page.getByRole('button',{name:'进入工作台'}).click();await page.getByRole('link',{name:'工单',exact:true}).waitFor()}
let base;
(async()=>{
 const temp=fs.mkdtempSync(path.join(os.tmpdir(),'flowdesk-browser-'));
 base='http://127.0.0.1:'+await port();let errors='';
 child=spawn(binary,['--workspace','--data-dir',temp,'--no-browser','--listen',base.replace('http://','')],{stdio:['pipe','ignore','pipe']});
 child.stderr.on('data',b=>{errors=(errors+b.toString()).slice(-10000)});child.stdin.end('owner@example.test\nOwner\n'+password+'\n'+password+'\n');
 for(let i=0;i<400;i++){
  if(child.exitCode!==null)throw Error('Program exited: '+errors.slice(-500));
  try{const r=await fetch(base+'/api/ready');if(r.ok)break}catch{}
  await new Promise(resolve=>setTimeout(resolve,50));if(i===399)throw Error('Startup deadline');
 }
 const launch={headless:true,args:['--no-sandbox','--disable-dev-shm-usage']};
 if(opts['chromium-executable'])launch.executablePath=opts['chromium-executable'];
 if(opts['args-module']){const loaded=require(opts['args-module']);launch.args=(loaded.default??loaded).args.filter(x=>x!=='--disable-web-security')}
 browser=await pw.chromium.launch(launch);
 const owner=await browser.newContext({viewport:{width:1440,height:1000},timezoneId:'Asia/Shanghai'});
 const page=await owner.newPage();const consoleErrors=[];page.on('pageerror',e=>consoleErrors.push(e.message));
 await page.goto(base);await page.getByLabel('邮箱',{exact:true}).waitFor();
 assert.equal(await page.getByText('快速体验',{exact:true}).count(),0);mark('personal login hides public demo account buttons and password');
 await login(page,'owner@example.test');await page.getByRole('heading',{name:'用自己的资料和工单开始'}).waitFor();mark('own administrator logs in and sees actual preparation checklist');
 async function data(context,method,url,body,status=200){
  const response=await context.request.fetch(base+url,{method,data:body,headers:{Origin:base,'Idempotency-Key':require('node:crypto').randomUUID()}});
  assert.equal(response.status(),status,await response.text());return (await response.json()).data;
 }
 for(const [email,role] of [['staff@example.test','agent'],['requester@example.test','requester']])await data(owner,'POST','/api/users',{email,display_name:role,role,password},201);
 await page.getByRole('link',{name:'公开资料',exact:true}).click();await page.getByRole('button',{name:'新增资料'}).click();
 const modal=page.getByRole('dialog',{name:'新增公开资料'});const body='# 连接失败\n\n请先重启客户端后重新连接。';
 await modal.locator('input[type=file]').setInputFiles({name:'连接说明.md',mimeType:'text/markdown',buffer:Buffer.from(body)});
 await modal.getByLabel(/^正文/).waitFor();
 for(let i=0;i<80&&await modal.getByLabel(/^正文/).inputValue()!==body;i++)await page.waitForTimeout(50);
 assert.equal(await modal.getByLabel(/^正文/).inputValue(),body);
 assert.equal(await modal.getByLabel('资料标题',{exact:true}).inputValue(),'连接说明');
 assert.equal((await data(owner,'GET','/api/knowledge')).total,0);mark('actual file picker imports UTF-8 Markdown without saving implicitly');
 await modal.getByRole('button',{name:'保存资料'}).click();await modal.waitFor({state:'hidden'});
 assert.equal((await data(owner,'GET','/api/knowledge')).total,1);mark('explicit save persists imported knowledge and shows completion feedback');
 const requester=await browser.newContext({viewport:{width:1440,height:1000},timezoneId:'Asia/Shanghai'});const customerPage=await requester.newPage();
 await login(customerPage,'requester@example.test');const ticket=await data(requester,'POST','/api/tickets',{title:'连接失败',description:'客户端连接失败，请帮助排查连接问题。',category:'technical',priority:'normal'},201);
 await page.goto(base+'/#/tickets/'+ticket.id);await page.getByRole('button',{name:'生成建议',exact:true}).waitFor();
 async function generateRepeated(p,count){
  for(let i=0;i<count;i++){
   const before=await p.locator('.draft-card').count();await p.getByRole('button',{name:'生成建议',exact:true}).click();
   await p.waitForFunction(n=>document.querySelectorAll('.draft-card').length===n,before+1,{timeout:10000});
   assert.equal(await p.getByRole('button',{name:'取消任务',exact:true}).count(),0);
   assert.equal(await p.getByText('任务独立在后台执行。离开页面不会取消任务。',{exact:true}).count(),0);
  }
 }
 await generateRepeated(page,5);mark('administrator rapid generations show new results and remove running-only cancellation controls');
 const staff=await browser.newContext({viewport:{width:1440,height:1000},timezoneId:'Asia/Shanghai'});const staffPage=await staff.newPage();
 staffPage.on('pageerror',e=>consoleErrors.push(e.message));await login(staffPage,'staff@example.test');await staffPage.goto(base+'/#/tickets/'+ticket.id);
 await staffPage.getByRole('button',{name:'生成建议',exact:true}).waitFor();await generateRepeated(staffPage,5);mark('staff rapid generations keep ten ordered history blocks without stale cancellation');
 await staffPage.getByRole('link',{name:'总览',exact:true}).click();await staffPage.goto(base+'/#/tickets/'+ticket.id);
 await staffPage.locator('.draft-card').first().waitFor();assert.equal(await staffPage.locator('.draft-card').count(),10);assert.equal(await staffPage.getByRole('button',{name:'取消任务',exact:true}).count(),0);mark('returning to a completed ticket preserves history and never reopens running controls');
 const selected=staffPage.locator('.draft-card.selected');const reply='已核对的正式回复：请重启客户端后重新连接。';
 assert(await selected.locator('textarea').isEditable(),'selected current draft must be editable');
 await selected.locator('textarea').fill(reply);
 for(const ack of await selected.locator('.acknowledgement input').all())await ack.check();
 await selected.getByRole('button',{name:'核对并发布',exact:true}).click();
 const preview=staffPage.getByRole('dialog',{name:'确认发布回复'});assert.equal(await preview.getByText(reply,{exact:true}).count(),1);
 await preview.getByRole('button',{name:'确认发布',exact:true}).click();
 const completion=staffPage.locator('.completion-notice');await completion.getByText('已核对并发布，申请人现在可以查看正式回复。',{exact:true}).waitFor();
 assert(await completion.isVisible());mark('publication success is visible immediately after confirmation and modal closes');
 await completion.getByRole('button',{name:'关闭成功提示'}).click();await completion.waitFor({state:'hidden'});mark('publication success notice can be closed with X');
 await staffPage.locator('.draft-card').getByText('已发布',{exact:true}).waitFor();mark('published draft retains explicit published status after the transient notice closes');
 await customerPage.goto(base+'/#/tickets/'+ticket.id);await customerPage.getByText(reply,{exact:true}).waitFor();
 assert.equal(await customerPage.locator('.draft-card').count(),0);mark('requester sees exact published text and never sees staff drafts');
 await staffPage.setViewportSize({width:390,height:844});await staffPage.waitForTimeout(250);
 assert(await staffPage.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+2));mark('390px ticket layout has no horizontal document overflow');
 await page.getByRole('link',{name:'模型设置',exact:true}).click();await page.getByRole('heading',{name:'按需要，接入你的模型'}).waitFor();
 assert.equal(await page.getByRole('button',{name:'基础连接测试'}).isDisabled(),true);mark('model connection test stays disabled until a real configuration is saved');
 await page.setViewportSize({width:390,height:844});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+2));mark('390px model configuration layout has no horizontal document overflow');
 if(opts['screenshots']){
  fs.mkdirSync(opts.screenshots,{recursive:true});await staffPage.screenshot({path:path.join(opts.screenshots,'ticket-mobile.png'),fullPage:true});
  await staffPage.setViewportSize({width:1440,height:1000});await staffPage.screenshot({path:path.join(opts.screenshots,'ticket-desktop.png'),fullPage:true});
 }
 assert.deepEqual(consoleErrors,[]);mark('tested browser pages have no uncaught JavaScript errors');
 const version=execFileSync(binary,['--version'],{encoding:'utf8'}).trim();const digest=version.match(/source=([a-f0-9]{64})/)?.[1]??'development';
 fs.mkdirSync(path.dirname(out),{recursive:true});fs.writeFileSync(out,JSON.stringify({source_input_sha256:digest,version_output:version,browser:browser.version(),checks,scope:'headless Chromium on Linux; temporary personal workspace; offline demo rules for UI generation; live protocol checked separately'},null,2)+'\n');
 console.log('Browser checks passed:',checks.length);
})().catch(e=>{console.error(e.stack);process.exitCode=1}).finally(async()=>{
 if(browser)await browser.close();if(child&&child.exitCode===null){child.kill('SIGINT');await new Promise(resolve=>{const timer=setTimeout(()=>{child.kill('SIGKILL');resolve()},12000);child.once('exit',()=>{clearTimeout(timer);resolve()})})}
});

// Isolated UI acceptance: no real mailbox credentials, providers or SMTP traffic.
const { createServer } = require('node:http');
const { readFile } = require('node:fs/promises');
const { resolve, extname } = require('node:path');
const { chromium } = require(process.env.MELDMAIL_PLAYWRIGHT_MODULE || '/tmp/meldmail-browser/node_modules/playwright');
const root = resolve(__dirname, '../internal/api/dist');
const accounts = [
 { id: 1, email: 'one@gmail.com', display_name: 'One', provider: 'gmail', auth_type: 'password', imap_host: 'imap.gmail.com', imap_port: 993, imap_tls: 'implicit', smtp_host: 'smtp.gmail.com', smtp_port: 465, smtp_tls: 'implicit', proxy_mode: 'global', status: 'error', last_error: 'TLS 连接或证书校验失败，尚未验证授权码；请检查网络与代理', last_sync_at: 1 },
 { id: 2, email: 'two@qq.com', display_name: 'Two', provider: 'qq', auth_type: 'password', imap_host: 'imap.qq.com', imap_port: 993, imap_tls: 'implicit', smtp_host: 'smtp.qq.com', smtp_port: 465, smtp_tls: 'implicit', proxy_mode: 'socks5', status: 'active', last_error: '', last_sync_at: 1 }
];
const emails = [[1,'inbox',false],[1,'inbox',true],[1,'archive',false],[1,'other',true],[1,'trash',false],[2,'inbox',false],[2,'archive',true],[2,'trash',true]].map(([account_id,folder_role,is_read],i)=>({id:i+1,account_id,folder_id:i+1,folder_role,is_read,is_starred:false,uid:i+1,subject:`Message ${i+1}`,from_name:'Sender',from_addr:'sender@example.com',snippet:'Fixture message',has_attachments:false,date:1700000000+i,to_addrs:'[]',cc_addrs:'[]'}));
const streams = new Set(); let importCalls = 0; let exportCalls = 0; let defaultProxy = ''; let managed = false;
function category(rows){return {inbox:rows.filter(x=>x.folder_role==='inbox').length,all:rows.filter(x=>x.folder_role!=='trash').length,unread:rows.filter(x=>x.folder_role!=='trash'&&!x.is_read).length,archive:rows.filter(x=>x.folder_role==='archive').length,trash:rows.filter(x=>x.folder_role==='trash').length};}
function summary(){return {total:emails.length,unread:emails.filter(e=>!e.is_read).length,categories:category(emails),accounts:accounts.map(a=>{const rows=emails.filter(e=>e.account_id===a.id);return {...a,total:rows.length,unread:rows.filter(e=>!e.is_read).length,categories:category(rows)}})};}
const envelope = {format:'meldmail-config',version:1,kdf:'pbkdf2-sha256-600000',salt:'Zml4dHVyZQ==',data:'ZW5jcnlwdGVkLWZpeHR1cmU='};
const server = createServer(async(req,res)=>{
 try {
 const url=new URL(req.url,'http://fixture');const path=url.pathname;
 const reply=(data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
 if(path==='/api/auth/me')return reply({authenticated:true});
 if(path==='/api/events'){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(': ready\n\n');streams.add(res);req.on('close',()=>streams.delete(res));return;}
 if(path==='/api/accounts'&&req.method==='GET')return reply(accounts);
 if(path==='/api/settings/proxy/edit')return reply({url:defaultProxy});
 if(path==='/api/accounts/2/proxy')return reply({url:'socks5://user:saved-secret@proxy.example:1080'});
 if(path==='/api/settings/proxy'){if(req.method==='PUT'){let data='';for await(const chunk of req)data+=chunk;defaultProxy=JSON.parse(data).url;}return reply({url:defaultProxy,managed});}
 if(['/api/mcp/approvals','/api/mcp/tokens','/api/mcp/audit'].includes(path))return reply([]);
 if(path==='/api/stats')return reply({mail:summary(),resources:{timestamp:1,memory:{used_bytes:1,limit_bytes:268435456,source:'container'},cpu:{percent:null,limit_cores:.5},disk:{available:true,data_bytes:1,volume_known:false}}});
 if(path==='/api/emails'){
  let rows=emails.filter(e=>url.searchParams.get('folder')==='all'?e.folder_role!=='trash':e.folder_role===(url.searchParams.get('folder')||'inbox'));
  if(url.searchParams.get('unread')==='true')rows=rows.filter(e=>!e.is_read);
  if(url.searchParams.get('account_id'))rows=rows.filter(e=>e.account_id===Number(url.searchParams.get('account_id')));
  return reply({emails:rows,next_cursor:'',total:rows.length});
 }
 if(path==='/api/accounts/export'||path==='/api/accounts/import'){
  let text='';for await(const chunk of req)text+=chunk;const body=JSON.parse(text);
  if(body.password!=='configuration passphrase')return reply({},400);
  if(path.endsWith('/export')){exportCalls++;return reply(envelope);}
  if(body.bundle.format!=='meldmail-config')return reply({},400);
  defaultProxy='socks5://imported.example:1080';importCalls++;return reply({added:0,updated:2,duplicates:1});
 }
 if(path.startsWith('/api/'))return reply({error:'unexpected fixture request'},404);
 const file=resolve(root,path==='/'?'index.html':'.'+path);if(!file.startsWith(root+'/'))return reply({},404);
 res.writeHead(200,{'Content-Type':{'.html':'text/html','.js':'application/javascript','.css':'text/css'}[extname(file)]||'application/octet-stream'});res.end(await readFile(file));
 }catch{res.writeHead(500);res.end('fixture error');}
});
(async()=>{
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await chromium.launch({executablePath:process.env.MELDMAIL_CHROME||'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
 try{
 const page=await browser.newPage({viewport:{width:1440,height:900},acceptDownloads:true});page.setDefaultTimeout(6000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.address().port}`);
 const counts=async()=>page.locator('.sidebar .account-count').allTextContents();
 await page.waitForFunction(()=>document.querySelector('.account-count')?.textContent==='3');
 const matchingControls=async()=>{
  await page.mouse.move(0,0);
  await page.waitForFunction(()=>{const controls=[...document.querySelectorAll('.workspace-toolbar .sync-button,.workspace-toolbar .theme-toggle')].map(el=>getComputedStyle(el));return controls.length===2&&controls[0].color===controls[1].color&&controls[0].backgroundColor===controls[1].backgroundColor});
  const metrics=await page.locator('.workspace-toolbar .sync-button,.workspace-toolbar .theme-toggle').evaluateAll(elements=>elements.map(el=>{const style=getComputedStyle(el),rect=el.getBoundingClientRect();return {width:rect.width,height:rect.height,radius:style.borderRadius,color:style.color,background:style.backgroundColor,stroke:el.querySelector('svg').getAttribute('stroke-width')}}));
  if(metrics.length!==2||JSON.stringify(metrics[0])!==JSON.stringify(metrics[1])||metrics[0].width!==metrics[0].height)throw Error('sync and theme controls do not match '+JSON.stringify(metrics));
 };
 await matchingControls();
 if(await page.locator('.workspace-toolbar .toolbar-settings').count())throw Error('duplicate settings entry remains');
 const footer=page.locator('.sidebar .footer-actions');
 const starts=await footer.locator('button').evaluateAll(items=>items.map(el=>({icon:el.querySelector('svg').getBoundingClientRect().x,text:el.querySelector('span').getBoundingClientRect().x})));
 if(starts.length!==4||starts.some(item=>item.icon!==starts[0].icon||item.text!==starts[0].text))throw Error('footer actions are misaligned');


 for(const [name,want] of [['未读邮件',['3','2','1']],['已归档',['2','1','1']],['废纸篓',['2','1','1']],['全部邮件',['6','4','2']],['统一收件箱',['3','2','1']]]){
  await page.getByRole('button',{name,exact:true}).click();const got=await counts();if(JSON.stringify(got)!==JSON.stringify(want))throw Error(`category counts stale: ${name}: ${got}`);
 }
 // SSE updates category counts without waiting for the resource polling timer.
 emails[0].is_read=true;
 for(const stream of streams)stream.write(`event: flag_updated\ndata: ${JSON.stringify({...emails[0],pending:false})}\n\n`);
 await page.getByRole('button',{name:'未读邮件',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('.account-count')?.textContent==='2');
 await page.getByRole('button',{name:'连接暂不可用 · one@gmail.com',exact:true}).click();
 const panel=page.getByRole('dialog',{name:'邮箱与连接',exact:true});await panel.getByRole('heading',{name:'编辑邮箱',exact:true}).waitFor();
 if(!await panel.getByText('连接暂不可用',{exact:false}).count())throw Error('connection explanation missing');
 const bounds=()=>panel.evaluate(el=>{const r=el.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}});
 const frame=await bounds();
 const stable=async()=>{if(JSON.stringify(await bounds())!==JSON.stringify(frame))throw Error('settings frame moved while switching content');};
 await panel.locator('.saved-accounts li').nth(1).getByRole('button').first().click();
 await page.waitForFunction(()=>[...document.querySelectorAll('input')].some(el=>el.value==='socks5://user:saved-secret@proxy.example:1080'));
 await stable();
 await panel.locator('.saved-accounts li').first().getByRole('button').first().click();
 await panel.getByRole('combobox',{name:/^连接方式/}).selectOption('socks5');
 await stable();
 if(await panel.getByLabel('代理 URL',{exact:true}).getAttribute('type')!=='text')throw Error('account proxy URL still masked');
 if(await panel.locator('.global-proxy-panel').count())throw Error('default proxy duplicated in mailbox form');
 await panel.getByRole('combobox',{name:/^连接方式/}).selectOption('direct');
 await panel.getByRole('button',{name:'共用代理（可选）',exact:true}).click();
 if(await panel.locator('.account-form').count())throw Error('mailbox and default proxy editors shown together');
 if(await panel.getByLabel('共用代理 URL',{exact:true}).getAttribute('type')!=='text')throw Error('default proxy URL still masked');
 await panel.getByLabel('共用代理 URL',{exact:true}).fill('http://proxy.example:7890');
 await panel.getByRole('button',{name:'保存共用代理',exact:true}).click();
 await panel.getByRole('status').filter({hasText:'共用代理已更新'}).waitFor();
 await stable();
 await panel.getByRole('button',{name:'导入 / 导出',exact:true}).click();await stable();
 await panel.getByRole('button',{name:'共用代理（可选）',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('.global-proxy-panel input')?.value==='http://proxy.example:7890');
 await stable();
 await panel.locator('.saved-accounts li').first().getByRole('button').first().click();
 await panel.getByRole('combobox',{name:/^连接方式/}).locator('option:checked').filter({hasText:'使用共用代理'}).waitFor({state:'attached'});

 await panel.getByRole('button',{name:'＋ 添加邮箱',exact:true}).click();
 if(await panel.getByRole('combobox',{name:/^连接方式/}).inputValue()!=='direct')throw Error('new mailbox does not default to direct');
 await panel.locator('.saved-accounts li').first().getByRole('button').first().click();
 const layout=await panel.evaluate(el=>({width:el.getBoundingClientRect().width,scrollbar:getComputedStyle(el).scrollbarWidth,overflow:el.scrollWidth>el.clientWidth}));
 if(layout.width<1000||layout.scrollbar!=='thin'||layout.overflow)throw Error('bad settings layout '+JSON.stringify(layout));
 await page.screenshot({path:'/tmp/meldmail-account-settings-wide.png'});
 if(await panel.locator('.connection-guide').getAttribute('open')!==null)throw Error('connection instructions not compact');
 await panel.getByRole('button',{name:'导入 / 导出',exact:true}).click();
 const transfer=panel.locator('.transfer-grid');const cards=transfer.locator('.transfer-card');
 const y=await cards.evaluateAll(items=>items.map(el=>el.getBoundingClientRect().y));if(y[0]!==y[1])throw Error('desktop transfer cards not horizontal');
 await panel.getByLabel('导出密码',{exact:true}).fill('configuration passphrase');const downloaded=page.waitForEvent('download');await panel.getByRole('button',{name:'下载加密配置',exact:true}).click();await downloaded;
 await panel.locator('input[type=file]').setInputFiles({name:'fixture-config.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(envelope))});
 await panel.getByLabel('导入密码',{exact:true}).fill('configuration passphrase');const proxyReload=page.waitForResponse(response=>response.url().endsWith('/api/settings/proxy')&&response.request().method()==='GET');await panel.getByRole('button',{name:'导入并合并',exact:true}).click();if((await (await proxyReload).json()).url!=='socks5://imported.example:1080')throw Error('imported default proxy not refreshed');await panel.getByRole('status').filter({hasText:'导入完成'}).waitFor();
 if(importCalls!==1||exportCalls!==1||await page.locator('.account-row').count()!==2)throw Error('configuration UI merge failed');
 await page.screenshot({path:'/tmp/meldmail-settings-wide.png'});
 await page.setViewportSize({width:390,height:844});
 await matchingControls();
 if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('mobile page overflow');
 if(await panel.evaluate(el=>el.getBoundingClientRect().width>innerWidth||el.scrollWidth>el.clientWidth))throw Error('mobile panel overflow');
 const mobileY=await cards.evaluateAll(items=>items.map(el=>el.getBoundingClientRect().y));if(mobileY[0]>=mobileY[1])throw Error('mobile cards not stacked');
 await page.screenshot({path:'/tmp/meldmail-settings-mobile.png'});
 for(const viewport of [{width:1024,height:600},{width:390,height:844}]){
  await page.setViewportSize(viewport);
  await panel.locator('.saved-accounts li').nth(1).getByRole('button').first().click();
  await page.waitForFunction(()=>[...document.querySelectorAll('input')].some(el=>el.value==='socks5://user:saved-secret@proxy.example:1080'));
  const select=panel.getByRole('combobox',{name:/^连接方式/});await select.scrollIntoViewIfNeeded();
  const dimensions=await select.evaluate(el=>{const r=el.getBoundingClientRect();return {width:r.width,bottom:r.bottom,height:innerHeight}});
  if(dimensions.width<180||dimensions.bottom>dimensions.height)throw Error('proxy selector clipped '+JSON.stringify(dimensions));
  const before=await bounds();await select.selectOption('direct');
  if(JSON.stringify(await bounds())!==JSON.stringify(before))throw Error('small viewport frame jumped');
  const save=panel.getByRole('button',{name:'保存邮箱',exact:true});
  if(!await save.isVisible()||await save.evaluate(el=>el.getBoundingClientRect().bottom>innerHeight))throw Error('settings actions outside viewport');
 }
 await panel.getByRole('button',{name:'关闭设置',exact:true}).click();await page.setViewportSize({width:1440,height:900});
 await page.getByRole('button',{name:'修改登录密码',exact:true}).click();
 const password=page.getByRole('dialog',{name:'修改登录密码',exact:true});
 const passwordY=await password.locator('label').evaluateAll(items=>items.slice(1).map(el=>el.getBoundingClientRect().y));if(passwordY[0]!==passwordY[1])throw Error('password fields not horizontal');
 await password.getByRole('button',{name:'关闭修改密码',exact:true}).click();
 await page.getByRole('button',{name:'＋ 撰写邮件',exact:true}).click();
 const composer=page.locator('dialog.composer');if(await composer.evaluate(el=>el.getBoundingClientRect().width)<900)throw Error('composer too narrow');
 await composer.getByRole('button',{name:'关闭撰写窗口',exact:true}).click();
 await page.getByRole('button',{name:/AI 与 MCP/}).click();
 const mcp=page.getByRole('dialog',{name:'AI 与 MCP',exact:true});await mcp.waitFor();
 if(await mcp.evaluate(el=>el.getBoundingClientRect().width)<900)throw Error('MCP modal too narrow');
 const mcpFrame=()=>mcp.evaluate(el=>{const r=el.getBoundingClientRect();return [r.x,r.y,r.width,r.height]});
 const initialMCP=await mcpFrame();
 for(const button of await mcp.locator('.mcp-tabs button').all()){
  await button.click();if(JSON.stringify(await mcpFrame())!==JSON.stringify(initialMCP))throw Error('MCP tab resized modal');
 }

 await mcp.getByRole('button',{name:'关闭 MCP 设置',exact:true}).click();
 await page.getByRole('button',{name:'切换为深色',exact:true}).click();
 await page.getByRole('button',{name:'邮箱与连接',exact:true}).click();
 if(await panel.evaluate(el=>getComputedStyle(el).backgroundColor)!=='rgb(37, 40, 45)')throw Error('dark modal theme mismatch');
 if(await panel.evaluate(el=>getComputedStyle(el).scrollbarColor)==='auto')throw Error('dark scrollbar unthemed');
 await panel.getByRole('button',{name:'关闭设置',exact:true}).click();await matchingControls();
 await page.locator('.workspace-toolbar').screenshot({path:'/tmp/meldmail-toolbar-dark.png'});

 managed=true;defaultProxy='http://127.0.0.1:7890';
 await page.getByRole('button',{name:'邮箱与连接',exact:true}).click();
 await panel.getByRole('button',{name:'共用代理（可选）',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('.global-proxy-panel input')?.value==='http://127.0.0.1:7890');
 if(await panel.getByLabel('共用代理 URL',{exact:true}).getAttribute('readonly')===null)throw Error('environment proxy is editable');
 if(await panel.getByRole('button',{name:'保存共用代理',exact:true}).count())throw Error('environment proxy can be overwritten');
 await panel.getByText('共用代理由 .env 管理，请修改 MELDMAIL_HOST_PROXY_PORT 后重新部署。',{exact:true}).waitFor();
 await panel.getByRole('button',{name:'关闭设置',exact:true}).click();
 if(errors.length)throw Error(errors.join('\n'));
 console.log('PASS: category-linked counts, SSE counts, actionable status, encrypted transfer UI, wide/thin-scrollbar modals, matching toolbar icons, visible proxy URLs and mobile layout');
 }finally{await browser.close();for(const stream of streams)stream.end();server.closeAllConnections();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;for(const stream of streams)stream.end();server.closeAllConnections();server.close();});

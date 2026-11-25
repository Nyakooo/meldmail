// Focused browser regression for create -> edit -> retest without re-entering credentials.
// Uses an isolated API fixture; never opens or changes a real mailbox.
const {createServer}=require('node:http');
const {readFile}=require('node:fs/promises');
const {resolve,extname}=require('node:path');
const {chromium}=require(process.env.MELDMAIL_PLAYWRIGHT_MODULE || '/tmp/meldmail-browser/node_modules/playwright');
const root=resolve(__dirname,'../internal/api/dist');
let saved=null, creates=0, updates=0, tests=0;
const server=createServer(async(req,res)=>{
 try {
  const path=new URL(req.url,'http://fixture').pathname;
  const reply=(data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
  if(path==='/api/auth/me')return reply({authenticated:true});
  if(path==='/api/events'){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(': fixture\n\n');return;}
  if(path==='/api/accounts' && req.method==='GET')return reply(saved?[saved]:[]);
  if(path==='/api/settings/proxy')return reply({configured:false,url:''});
  if(path==='/api/mcp/approvals')return reply([]);
  if(path==='/api/emails')return reply({emails:[],next_cursor:''});
  if(path==='/api/accounts'||path==='/api/accounts/1'||path==='/api/accounts/test'){
   let body='';for await(const chunk of req){body+=chunk;if(body.length>65536)throw Error('fixture request too large');}
   const input=JSON.parse(body);
   if(path==='/api/accounts/test'){
    tests++;
    if(input.id!==1 || input.password)return reply({error:'saved credentials not reused'},400);
    return reply({diagnostics:[{stage:'imap',ok:true,message:'已使用保存的凭据'}]});
   }
   if(req.method==='POST'){
    creates++;if(!input.password)return reply('account: credentials are required',400);
    saved={...input,id:1,imap_host:'imap.qq.com',imap_port:993,imap_tls:'implicit',smtp_host:'smtp.qq.com',smtp_port:465,smtp_tls:'implicit',last_error:'',last_sync_at:0};delete saved.password;delete saved.access_token;
    return reply(saved,201);
   }
   if(req.method==='PUT'){
    updates++;if(input.id!==1||input.password)return reply({error:'unexpected edit payload'},400);
    saved={...saved,...input};return reply(saved);
   }
  }
  if(path.startsWith('/api/'))return reply({error:'unexpected route'},404);
  const file=resolve(root,path==='/'?'index.html':'.'+path);
  if(!file.startsWith(root+'/'))return reply({},404);
  const data=await readFile(file);res.writeHead(200,{'Content-Type':{'.html':'text/html','.js':'application/javascript','.css':'text/css'}[extname(file)]||'application/octet-stream'});res.end(data);
 }catch{res.writeHead(500);res.end('fixture failure');}
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({executablePath:process.env.MELDMAIL_CHROME||'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
 try {
  const page=await browser.newPage();page.setDefaultTimeout(4000);
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.getByRole('button',{name:'邮箱与连接',exact:true}).click();const panel=page.getByRole('dialog',{name:'邮箱与连接',exact:true});
  await panel.getByRole('combobox',{name:/服务商/}).selectOption('qq');
  await panel.getByLabel('邮箱地址',{exact:true}).fill('fixture@qq.com');await panel.getByLabel('密码或应用授权码',{exact:true}).fill('fixture-authorization');
  await panel.getByRole('button',{name:'保存邮箱',exact:true}).click();await panel.getByRole('heading',{name:'编辑邮箱',exact:true}).waitFor();
  if(await panel.getByLabel('密码或应用授权码',{exact:true}).inputValue())throw Error('credential was not cleared');
  await panel.getByRole('button',{name:'保存邮箱',exact:true}).click();await page.waitForFunction(()=>document.querySelector('[role="status"].notice')?.textContent.includes('设置已更新'));
  await panel.getByRole('button',{name:'测试连接',exact:true}).click();await panel.getByText('✓ imap · 已使用保存的凭据',{exact:true}).waitFor();
  if(creates!==1||updates!==1||tests!==1)throw Error('create/edit/test request boundary violated');
  await panel.getByRole('button',{name:'＋ 添加邮箱',exact:true}).click();await panel.getByRole('combobox',{name:/服务商/}).selectOption('qq');await panel.getByLabel('邮箱地址',{exact:true}).fill('new@qq.com');
  await panel.getByRole('button',{name:'测试连接',exact:true}).click();await panel.getByRole('alert').getByText(/请填写.*授权码/).waitFor();if(tests!==1)throw Error('empty new credentials sent to API');
  await page.setViewportSize({width:390,height:844});if(await panel.evaluate(el=>el.scrollWidth>el.clientWidth))throw Error('mobile panel overflow');
  console.log('PASS: saved account switches to edit, blank credentials retained on repeat save/test, empty new credentials explained locally, mobile layout');
 }finally{await browser.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
})().catch(err=>{console.error(err.message);server.closeAllConnections();server.close();process.exitCode=1;});

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"github.com/nyakooo/meldmail/internal/testmail"
	"golang.org/x/crypto/bcrypt"
)

func TestMCPApprovalBrowser(t *testing.T) {
	if os.Getenv("MELDMAIL_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set MELDMAIL_PLAYWRIGHT_MODULE for browser acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	fixture := testmail.IMAP(t, "agent@example.com", "fixture")
	smtpPort, deliveries := testmail.SMTP(t, fixture.ServerTLS)
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "approval-browser.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	service, err := mail.New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service.TLSConfig = fixture.ClientTLS
	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-admin"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitPassword(ctx, string(hash)); err != nil {
		t.Fatal(err)
	}
	account, err := service.Prepare(ctx, mail.AccountInput{Account: storage.Account{Email: "agent@example.com", Provider: "custom", IMAPHost: "127.0.0.1", IMAPPort: fixture.Port, IMAPTLS: "implicit", SMTPHost: "127.0.0.1", SMTPPort: smtpPort, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	account, err = store.SaveAccount(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(store, false, service)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("GET /fixture/deliveries", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]int{"count": len(deliveries)}) })
	server := httptest.NewServer(mux)
	defer server.Close()
	defer server.CloseClientConnections()
	script := `
const {chromium}=require(process.env.MELDMAIL_PLAYWRIGHT_MODULE);
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.MELDMAIL_CHROME||'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:960}});page.setDefaultTimeout(6000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const base=process.env.MELDMAIL_BROWSER_URL;
  await page.goto(base);await page.getByLabel('管理员密码',{exact:true}).fill('fixture-admin');await page.getByRole('button',{name:'登录工作台 →',exact:true}).click();await page.getByText('● 实时更新已连接',{exact:true}).waitFor();
  await page.getByRole('button',{name:/AI 与 MCP/}).click();await page.getByLabel('令牌名称',{exact:true}).fill('Browser Agent');await page.getByLabel('权限',{exact:true}).selectOption('readwrite');
  const [created]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/mcp/tokens'&&r.request().method()==='POST'),page.getByRole('button',{name:'创建令牌',exact:true}).click()]);
  const token=(await created.json()).token;if(!token)throw new Error('token not issued');if(await page.getByLabel('新访问令牌',{exact:true}).getAttribute('type')!=='password')throw new Error('token not masked');
  await page.getByLabel('关闭 MCP 设置',{exact:true}).click();await page.locator('dialog.mcp-panel').waitFor({state:'detached'});
  let id=1;
  const call=async(args)=>{const response=await page.request.post(base+'/mcp',{headers:{Authorization:'Bearer '+token,Accept:'application/json, text/event-stream','Content-Type':'application/json'},data:{jsonrpc:'2.0',id:id++,method:'tools/call',params:{name:'send_email',arguments:args}}});if(!response.ok())throw new Error('MCP call failed: '+response.status());const body=await response.json();return JSON.parse(body.result.content[0].text)};
  const accounts=await (await page.request.get(base+'/api/accounts')).json();const account_id=accounts[0].id;
  const pending=await call({account_id,to:['recipient@example.com'],subject:'Approval from MCP',text:'Approve body from MCP'});if(pending.status!=='pending_approval')throw new Error('send not queued');
  const review=page.locator('dialog.approval-review');await review.getByText('Approve body from MCP',{exact:true}).waitFor();if((await (await page.request.get(base+'/fixture/deliveries')).json()).count!==0)throw new Error('sent before approval');
  await review.getByRole('button',{name:'批准并发送',exact:true}).click();await review.waitFor({state:'detached'});if((await (await page.request.get(base+'/fixture/deliveries')).json()).count!==1)throw new Error('approval did not send once');
  await call({account_id,to:['reject@example.com'],subject:'Reject fixture',html:'<script>window.hacked=true</script>'});await review.getByText('<script>window.hacked=true</script>',{exact:true}).waitFor();if(await review.locator('script').count())throw new Error('approval HTML executed');await review.getByRole('button',{name:'拒绝',exact:true}).click();await review.waitFor({state:'detached'});
  await page.setViewportSize({width:390,height:844});await page.getByLabel('打开导航',{exact:true}).click();await page.getByRole('button',{name:/AI 与 MCP/}).click();await page.getByRole('button',{name:'审计日志',exact:true}).click();await page.getByText('approval_rejected',{exact:true}).waitFor();
  const audit=await page.locator('.audit-records').innerText();if(audit.includes(token)||audit.includes('Approve body from MCP'))throw new Error('audit exposed secret/body');
  await page.getByRole('button',{name:'访问令牌',exact:true}).click();page.once('dialog',d=>d.accept());await page.getByRole('button',{name:'撤销 Browser Agent',exact:true}).click();await page.getByRole('button',{name:'撤销 Browser Agent',exact:true}).waitFor({state:'detached'});
  const revoked=await page.request.post(base+'/mcp',{headers:{Authorization:'Bearer '+token,Accept:'application/json, text/event-stream','Content-Type':'application/json'},data:{jsonrpc:'2.0',id:99,method:'tools/list'}});if(revoked.status()!==401)throw new Error('revocation ineffective');
  if((await (await page.request.get(base+'/fixture/deliveries')).json()).count!==1)throw new Error('reject or revoke sent mail');if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw new Error('mobile overflow');if(errors.length)throw new Error(errors.join('\n'));
  console.log('PASS: web token creation, SSE approval popup, explicit approve -> SMTP once, rejection, audit redaction, mobile token revocation');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
`
	scriptPath := filepath.Join(t.TempDir(), "mcp-browser.cjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", scriptPath)
	cmd.Env = append(os.Environ(), "MELDMAIL_BROWSER_URL="+server.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
	if len(deliveries) != 1 {
		t.Fatal("expected exactly one approved SMTP delivery")
	}
}

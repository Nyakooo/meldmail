package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"github.com/nyakooo/meldmail/internal/testmail"
	"golang.org/x/crypto/bcrypt"
)

func TestMailActionsBrowser(t *testing.T) {
	if os.Getenv("MELDMAIL_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set MELDMAIL_PLAYWRIGHT_MODULE for browser acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	fixture := testmail.IMAP(t, "browser@example.com", "fixture")
	smtpPort, deliveries := testmail.SMTP(t, fixture.ServerTLS)
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "mail-browser.db"))
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
	raw := "From: Sender <sender@example.com>\r\nTo: browser@example.com\r\nSubject: Browser HTML fixture\r\nMessage-ID: <browser-original@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=fixture\r\n\r\n--fixture\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<h1>HTML body</h1><p>Safe browser content</p><script>window.hacked=true</script><img src=\"https://tracker.invalid/pixel\">\r\n--fixture\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=fixture.bin\r\nContent-Transfer-Encoding: base64\r\n\r\naGVsbG8=\r\n--fixture--\r\n"
	if _, err := fixture.User.Append("INBOX", strings.NewReader(raw), &imap.AppendOptions{Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	account, err := service.Prepare(ctx, mail.AccountInput{Account: storage.Account{Email: "browser@example.com", Provider: "custom", IMAPHost: "127.0.0.1", IMAPPort: fixture.Port, IMAPTLS: "implicit", SMTPHost: "127.0.0.1", SMTPPort: smtpPort, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	account, err = store.SaveAccount(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Sync(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	workerCtx, stop := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				workerDone <- nil
				return
			case <-ticker.C:
				if err := service.ProcessFlagActions(workerCtx); err != nil {
					if workerCtx.Err() != nil {
						err = nil
					}
					workerDone <- err
					return
				}
			}
		}
	}()
	defer func() {
		stop()
		if err := <-workerDone; err != nil {
			t.Error(err)
		}
	}()
	h, err := New(store, false, service)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("GET /fixture/settled", func(w http.ResponseWriter, r *http.Request) {
		actions, err := store.FlagActions(r.Context())
		if err != nil {
			http.Error(w, "failed", 500)
			return
		}
		writeJSON(w, map[string]int{"pending": len(actions)})
	})

	mux.HandleFunc("POST /fixture/incoming", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fixture.User.Append("INBOX", strings.NewReader("From: new@example.com\r\nSubject: New Inbox arrival\r\nContent-Type: text/plain\r\n\r\nInbox only"), &imap.AppendOptions{Time: time.Now()}); err != nil {
			t.Error(err)
			http.Error(w, "failed", 500)
			return
		}
		if _, err := service.Sync(r.Context(), account.ID); err != nil {
			t.Error(err)
			http.Error(w, "failed", 500)
			return
		}
		w.WriteHeader(204)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	defer server.CloseClientConnections()
	script := `
const {chromium}=require(process.env.MELDMAIL_PLAYWRIGHT_MODULE);
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.MELDMAIL_CHROME||'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:960}});page.setDefaultTimeout(5000);const errors=[];let tracker=false;
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.url().includes('tracker.invalid'))tracker=true});
  const settled=async()=>{for(let i=0;i<200;i++){const response=await page.request.get(process.env.MELDMAIL_BROWSER_URL+'/fixture/settled');if((await response.json()).pending===0)return;await new Promise(r=>setTimeout(r,10))}throw new Error('flags did not settle')};
  await page.addInitScript(()=>{window.__mailEventCount=0;const Native=window.EventSource;window.EventSource=class extends Native{constructor(...args){super(...args);this.addEventListener('new_email',()=>{window.__mailEventCount++})}}});
  await page.goto(process.env.MELDMAIL_BROWSER_URL);await page.getByLabel('管理员密码',{exact:true}).fill('fixture-admin');await page.getByRole('button',{name:'登录工作台 →',exact:true}).click();
  await page.locator('.email-item').first().click();
  await page.frameLocator('iframe[title="邮件正文"]').getByRole('heading',{name:'HTML body',exact:true}).waitFor();
  await page.locator('.cache-status').filter({hasText:'正文已保存到本地，下次直接读取'}).waitFor();
  await page.getByRole('button',{name:'切换为深色',exact:true}).click();await page.frameLocator('iframe[title="邮件正文"]').getByRole('heading',{name:'HTML body',exact:true}).waitFor();
  await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');
  const frame=page.frames().find(f=>f.parentFrame());await frame.waitForFunction(()=>getComputedStyle(document.body).backgroundColor==='rgb(37, 40, 45)');
  if(process.env.MELDMAIL_STATS_SCREENSHOT)await page.screenshot({path:process.env.MELDMAIL_STATS_SCREENSHOT.replace('.png','-reading-dark.png')});
  await page.getByRole('button',{name:'← 返回列表',exact:true}).click();await page.locator('.email-item').first().click();await page.locator('.cache-status').filter({hasText:'已从本地缓存读取'}).waitFor();
  await page.getByRole('button',{name:'切换为浅色',exact:true}).click();
  if(await page.frameLocator('iframe[title="邮件正文"]').locator('script,img').count())throw new Error('unsafe HTML retained');
  const attachment=page.getByRole('link',{name:'↓ fixture.bin',exact:false});const href=await attachment.getAttribute('href');const download=await page.request.get(process.env.MELDMAIL_BROWSER_URL+href);
  if(!download.ok()||(await download.body()).toString()!=='hello'||!download.headers()['content-disposition'].includes('attachment'))throw new Error('attachment download failed');
  await settled();
  await page.getByRole('button',{name:'＋ 撰写邮件',exact:true}).click();await page.getByLabel('收件人',{exact:true}).fill('recipient@example.com');await page.getByLabel('主题',{exact:true}).fill('A new browser message');await page.getByLabel('正文',{exact:true}).fill('Composed in the browser');await page.getByRole('button',{name:'发送邮件 →',exact:true}).click();await page.locator('dialog.composer').waitFor({state:'detached'});
  for(const action of ['回复','转发']){await page.getByRole('button',{name:action,exact:true}).click();if(action==='转发')await page.getByLabel('收件人',{exact:true}).fill('forward@example.com');await page.getByLabel('正文',{exact:true}).fill('A browser note');await page.getByRole('button',{name:'发送邮件 →',exact:true}).click();await page.locator('dialog.composer').waitFor({state:'detached'})}
  await page.getByRole('button',{name:'☆ 星标',exact:true}).click();await settled();await page.getByRole('button',{name:'★ 取消星标',exact:true}).waitFor();
  await page.getByRole('button',{name:'归档',exact:true}).click();await settled();await page.waitForFunction(()=>document.querySelectorAll('.email-item').length===0);
  await page.getByRole('button',{name:/已归档/}).click();await page.locator('.email-item').first().click();await page.frameLocator('iframe[title="邮件正文"]').getByRole('heading',{name:'HTML body',exact:true}).waitFor();
  const incoming=async()=>{const before=await page.evaluate(()=>window.__mailEventCount);const response=await page.request.post(process.env.MELDMAIL_BROWSER_URL+'/fixture/incoming');if(!response.ok())throw new Error('incoming fixture failed');await page.waitForFunction(n=>window.__mailEventCount>n,before);if(await page.locator('.email-item').count()!==1)throw new Error('Inbox arrival leaked into a different folder')};await incoming();
  await page.setViewportSize({width:390,height:844});page.once('dialog',dialog=>dialog.accept());await page.getByRole('button',{name:'删除',exact:true}).click();await settled();await page.getByLabel('打开导航',{exact:true}).click();await page.getByRole('button',{name:/废纸篓/}).click();await page.waitForFunction(()=>document.querySelectorAll('.email-item').length===1);
  await incoming();
  if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw new Error('mobile overflow');if(tracker)throw new Error('remote tracker requested');if(errors.length)throw new Error(errors.join('\n'));
  console.log('PASS: HTML reading, authenticated attachment download, compose/reply/forward, star/archive/trash, desktop/mobile; no XSS/tracker requests');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
`
	scriptPath := filepath.Join(t.TempDir(), "mail-browser.cjs")
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
	for i := 0; i < 3; i++ {
		select {
		case raw := <-deliveries:
			message, err := netmail.ReadMessage(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if i == 1 && message.Header.Get("In-Reply-To") != "<browser-original@example.com>" {
				t.Fatal("browser reply threading missing")
			}
			if _, err := io.Copy(io.Discard, message.Body); err != nil && !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("browser SMTP delivery missing")
		}
	}
}

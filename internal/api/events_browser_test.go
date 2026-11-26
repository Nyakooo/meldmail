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
	"golang.org/x/crypto/bcrypt"
)

func TestLiveInboxBrowser(t *testing.T) {
	module := os.Getenv("MELDMAIL_PLAYWRIGHT_MODULE")
	if module == "" {
		t.Skip("set MELDMAIL_PLAYWRIGHT_MODULE to the installed playwright module for browser acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "browser.db"))
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
	hash, err := bcrypt.GenerateFromPassword([]byte("browser-fixture-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitPassword(ctx, string(hash)); err != nil {
		t.Fatal(err)
	}
	a, err := service.Prepare(ctx, mail.AccountInput{Account: storage.Account{Email: "fixture@gmail.com", Provider: "gmail", DisplayName: "Fixture"}, Password: "fixture-only"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err = store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.PrepareFolder(ctx, a.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.SaveMetadata(ctx, folder, storage.Email{UID: 1, Subject: "Existing fixture", Date: 1, ToAddrs: "[]", CCAddrs: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(store, false, service)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("POST /fixture/new", func(w http.ResponseWriter, r *http.Request) {
		e, err := store.SaveMetadata(r.Context(), folder, storage.Email{UID: 2, Subject: "Live SSE email", Date: 2, ToAddrs: "[]", CCAddrs: "[]"})
		if err != nil {
			t.Error(err)
			http.Error(w, "failed", 500)
			return
		}
		if err := service.Events.Publish("new_email", e); err != nil {
			t.Error(err)
			http.Error(w, "failed", 500)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /fixture/read", func(w http.ResponseWriter, r *http.Request) {
		flags := storage.EmailFlags{ID: first.ID, IsRead: true}
		if err := store.ApplyRemoteFlags(r.Context(), folder.ID, flags); err != nil {
			t.Error(err)
			http.Error(w, "failed", 500)
			return
		}
		if err := service.Events.Publish("flag_updated", flags); err != nil {
			t.Error(err)
			http.Error(w, "failed", 500)
			return
		}
		w.WriteHeader(204)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	script := `
const {chromium}=require(process.env.MELDMAIL_PLAYWRIGHT_MODULE);
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.MELDMAIL_CHROME || '/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:900}});let requests=0,completed=0;const errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  page.on('request',r=>{if(new URL(r.url()).pathname==='/api/emails')requests++});
  page.on('response',async r=>{if(new URL(r.url()).pathname==='/api/emails'){await r.finished();completed++}});
  await page.goto(process.env.MELDMAIL_BROWSER_URL);
  await page.getByLabel('登录密码',{exact:true}).fill('browser-fixture-password');
  await page.getByRole('button',{name:'登录工作台 →',exact:true}).click();
  await page.getByText('实时更新已连接',{exact:true}).waitFor();
  await page.waitForFunction(()=>document.querySelectorAll('.email-item').length===1);
  const deadline=Date.now()+3000;while(completed<2&&Date.now()<deadline)await new Promise(r=>setTimeout(r,10));
  if(completed<2)throw new Error('SSE connect snapshot missing');
  const before=requests;
  const added=await page.request.post(process.env.MELDMAIL_BROWSER_URL+'/fixture/new');if(!added.ok())throw new Error('fixture publish failed');
  await page.waitForFunction(()=>document.querySelectorAll('.email-item').length===2);
  if(requests!==before)throw new Error('new event caused a full list request instead of insertion');
  if(await page.locator('.email-item').first().getByText('Live SSE email',{exact:true}).count()!==1)throw new Error('new mail not inserted in descending order');
  await page.setViewportSize({width:390,height:844});
  const marked=await page.request.post(process.env.MELDMAIL_BROWSER_URL+'/fixture/read');if(!marked.ok())throw new Error('flag publish failed');
  await page.waitForFunction(()=>document.querySelectorAll('.email-item.unread').length===1);
  if(requests!==before)throw new Error('flag event caused full polling');
  if(errors.length)throw new Error(errors.join('\n'));
  console.log('PASS: real SSE inserted new email and updated read flag without refresh/list polling on desktop/mobile');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
`
	path := filepath.Join(t.TempDir(), "sse-browser.cjs")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", path)
	cmd.Env = append(os.Environ(), "MELDMAIL_BROWSER_URL="+server.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
}

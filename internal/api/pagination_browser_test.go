package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nyakooo/meldmail/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatisticsAndPaginationBrowser(t *testing.T) {
	if os.Getenv("MELDMAIL_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set MELDMAIL_PLAYWRIGHT_MODULE for browser acceptance")
	}
	store, handler := passwordFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var firstFolder storage.Folder
	for n := 1; n <= 2; n++ {
		account, err := store.SaveAccount(ctx, storage.Account{Email: fmt.Sprintf("page%d@example.com", n), Provider: "custom", AuthType: "password", IMAPHost: "fixture", IMAPPort: 993, IMAPTLS: "implicit", SMTPHost: "fixture", SMTPPort: 465, SMTPTLS: "implicit", CredentialsEnc: []byte("fixture"), ProxyMode: "direct", Status: "paused"})
		if err != nil {
			t.Fatal(err)
		}
		folder, err := store.PrepareFolder(ctx, account.ID, "INBOX", "inbox", 1)
		if err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			firstFolder = folder
		}
		start, end := 1, 65
		if n == 2 {
			start, end = 66, 120
		}
		for i := start; i <= end; i++ {
			if _, err := store.SaveMetadata(ctx, folder, storage.Email{UID: uint32(i), Subject: fmt.Sprintf("Mail %03d", i), FromAddr: "sender@example.com", ToAddrs: "[]", CCAddrs: "[]", Snippet: "pagination fixture", IsRead: i%2 == 0, Date: int64(i)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	events := make(chan storage.Email, 1)
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	// The fixture injects SSE independently so the pagination boundary is tested
	// without a remote IMAP server or modifying a real mailbox.
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, ": ready\n\n"); err != nil {
			return
		}
		controller := http.NewResponseController(w)
		if err := controller.Flush(); err != nil {
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case email := <-events:
				data, err := json.Marshal(email)
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := fmt.Fprintf(w, "event: new_email\ndata: %s\n\n", data); err != nil {
					return
				}
				if err := controller.Flush(); err != nil {
					return
				}
			}
		}
	})
	mux.HandleFunc("POST /fixture/new", func(w http.ResponseWriter, r *http.Request) {
		e, err := store.SaveMetadata(r.Context(), firstFolder, storage.Email{UID: 121, Subject: "Mail 121", FromAddr: "sender@example.com", ToAddrs: "[]", CCAddrs: "[]", Date: 121})
		if err != nil {
			t.Error(err)
			http.Error(w, "fixture failed", 500)
			return
		}
		select {
		case events <- e:
		case <-r.Context().Done():
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	defer server.CloseClientConnections()
	script := `
const {chromium}=require(process.env.MELDMAIL_PLAYWRIGHT_MODULE);
(async()=>{const browser=await chromium.launch({executablePath:process.env.MELDMAIL_CHROME||'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});try{
 const page=await browser.newPage({viewport:{width:1440,height:1000}});page.setDefaultTimeout(6000);const errors=[];page.on('pageerror',e=>errors.push(e.message));const base=process.env.MELDMAIL_BROWSER_URL;
 await page.goto(base);await page.getByLabel('管理员密码',{exact:true}).fill('original-fixture');await page.getByRole('button',{name:'登录工作台 →',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('.email-item').length===50);await page.locator('.system-strip').getByText('120 封',{exact:true}).waitFor();
 const pager=page.getByRole('navigation',{name:'邮件分页',exact:true});await pager.getByRole('button',{name:'下一页',exact:true}).click();await pager.getByText('第 2 页',{exact:true}).waitFor();await page.locator('.email-subject strong').first().getByText('Mail 070',{exact:true}).waitFor();
 const added=await page.request.post(base+'/fixture/new');if(!added.ok())throw Error('SSE fixture failed');await page.locator('.filtered-total').getByText('当前筛选共 121 封',{exact:true}).waitFor();if(await page.locator('.email-item').count()!==50||await page.getByText('Mail 121',{exact:true}).count())throw Error('new email shifted a later page');
 await pager.getByRole('button',{name:'上一页',exact:true}).click();await pager.getByText('第 1 页',{exact:true}).waitFor();await page.locator('.email-subject strong').first().getByText('Mail 121',{exact:true}).waitFor();
 await page.locator('.account-link').filter({hasText:'page1@example.com'}).click();await page.locator('.filtered-total').getByText('当前筛选共 66 封',{exact:true}).waitFor();await pager.getByRole('button',{name:'下一页',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('.email-item').length===16);await page.locator('.email-subject strong').first().getByText('Mail 016',{exact:true}).waitFor();
 await page.getByLabel('搜索邮件',{exact:true}).fill('Mail 065');await page.waitForFunction(()=>document.querySelectorAll('.email-item').length===1);await pager.getByText('第 1 页',{exact:true}).waitFor();await page.locator('.filtered-total').getByText('当前筛选共 1 封',{exact:true}).waitFor();
 const accounts=await page.request.get(base+'/api/stats');const summary=(await accounts.json()).mail;if(summary.total!==121||summary.accounts[0].total!==66||summary.accounts[1].total!==55)throw Error('account summary incorrect');
 const icon=await page.locator('link[rel="icon"]').getAttribute('href');if(!icon.includes('?v='))throw Error('favicon cache not versioned');const svg=await (await page.request.get(base+icon)).text();if(!svg.includes('meldmail-gradient'))throw Error('latest logo not served');
 await page.getByLabel('外观主题',{exact:true}).selectOption('dark');await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');await page.reload();await page.locator('.email-item').first().waitFor();await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');
 if(await page.locator('.system-strip').innerText().then(t=>t.includes('CPU')))throw Error('CPU still visible');
 if(process.env.MELDMAIL_STATS_SCREENSHOT)await page.screenshot({path:process.env.MELDMAIL_STATS_SCREENSHOT.replace('.png','-dark.png')});
 await page.getByLabel('外观主题',{exact:true}).selectOption('light');await page.waitForFunction(()=>document.documentElement.dataset.theme==='light');
 if(process.env.MELDMAIL_STATS_SCREENSHOT)await page.screenshot({path:process.env.MELDMAIL_STATS_SCREENSHOT});await page.setViewportSize({width:390,height:844});if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('mobile overflow');if(await page.locator('.system-strip>div').count()!==3)throw Error('missing metric cards');await page.getByRole('navigation',{name:'移动端导航',exact:true}).waitFor();await page.getByRole('navigation',{name:'移动端导航',exact:true}).getByRole('button',{name:'邮箱',exact:true}).click();await page.getByRole('heading',{name:'邮箱与连接',exact:true}).waitFor();
 if(process.env.MELDMAIL_STATS_SCREENSHOT)await page.screenshot({path:process.env.MELDMAIL_STATS_SCREENSHOT.replace('.png','-mobile.png')});if(errors.length)throw Error(errors.join('\n'));
 console.log('PASS: metrics/account totals, next/previous/filter/search pagination, stable later page on SSE, responsive layout and current favicon');
}finally{await browser.close()}})().catch(e=>{console.error(e.message);process.exit(1)});`
	path := filepath.Join(t.TempDir(), "pagination-browser.cjs")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", path)
	cmd.Env = append(os.Environ(), "MELDMAIL_BROWSER_URL="+server.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pagination browser: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	t.Log(string(output))
}

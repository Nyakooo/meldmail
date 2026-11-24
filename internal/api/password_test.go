package api

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func passwordFixture(t *testing.T) (*storage.Store, http.Handler) {
	t.Helper()
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "password.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	})
	hash, err := bcrypt.GenerateFromPassword([]byte("original-fixture"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitPassword(context.Background(), string(hash)); err != nil {
		t.Fatal(err)
	}
	service, err := mail.New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(store, false, service)
	if err != nil {
		t.Fatal(err)
	}
	return store, handler
}
func TestChangePassword(t *testing.T) {
	store, handler := passwordFixture(t)
	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	change := func(current, next string, cookie *http.Cookie) *httptest.ResponseRecorder {
		data, err := json.Marshal(map[string]string{"current_password": current, "new_password": next})
		if err != nil {
			t.Fatal(err)
		}
		return request("POST", "/api/auth/password", string(data), cookie)
	}
	if got := change("original-fixture", "new-fixture", nil); got.Code != 401 {
		t.Fatalf("unauthenticated change: %d", got.Code)
	}
	login := func(password string) *http.Cookie {
		body, err := json.Marshal(map[string]string{"password": password})
		if err != nil {
			t.Fatal(err)
		}
		w := request("POST", "/api/auth/login", string(body), nil)
		if w.Code != 200 {
			t.Fatalf("fixture login: %d", w.Code)
		}
		return w.Result().Cookies()[0]
	}
	first, second := login("original-fixture"), login("original-fixture")
	if got := change("wrong", "new-fixture", first); got.Code != 400 {
		t.Fatalf("wrong current: %d", got.Code)
	}
	if got := request("GET", "/api/auth/me", "", first); got.Code != 200 {
		t.Fatal("wrong current password expired session")
	}
	if got := change("original-fixture", "", first); got.Code != 400 {
		t.Fatal("empty new password accepted")
	}
	if got := change("original-fixture", strings.Repeat("界", 25), first); got.Code != 400 {
		t.Fatal("bcrypt byte limit not enforced")
	}
	if got := request("POST", "/api/auth/password", `{"current_password":"original-fixture","new_password":"new-fixture"} {}`, first); got.Code != 400 {
		t.Fatal("trailing JSON accepted")
	}
	cross := httptest.NewRequest("POST", "/api/auth/password", strings.NewReader(`{}`))
	cross.AddCookie(first)
	cross.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, cross)
	if w.Code != 403 {
		t.Fatal("cross-origin change accepted")
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	streamRequest, err := http.NewRequest("GET", server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	streamRequest.AddCookie(second)
	client := &http.Client{Timeout: 5 * time.Second}
	stream, err := client.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stream.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	reader := bufio.NewReader(stream.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\n" {
			break
		}
	}
	ended := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, reader); ended <- err }()
	changed := change("original-fixture", "new-fixture", first)
	if changed.Code != 200 {
		t.Fatalf("change: %d %s", changed.Code, changed.Body.String())
	}
	if changed.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("session cookie not cleared")
	}
	for _, cookie := range []*http.Cookie{first, second} {
		if got := request("GET", "/api/auth/me", "", cookie); got.Code != 401 {
			t.Fatal("old session remains valid")
		}
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old authenticated SSE stream did not close")
	}
	if got := request("POST", "/api/auth/login", `{"password":"original-fixture"}`, nil); got.Code != 401 {
		t.Fatal("old password still works")
	}
	current := login("new-fixture")
	hash, err := store.Setting(context.Background(), "admin_password")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "new-fixture" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-fixture")) != nil {
		t.Fatal("new password not stored as bcrypt hash")
	}
	restarted, err := New(store, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"new-fixture"}`))
	persisted := httptest.NewRecorder()
	restarted.ServeHTTP(persisted, r)
	if persisted.Code != 200 {
		t.Fatal("new handler lost persisted password")
	}
	for i := 0; i < 5; i++ {
		if got := change("wrong", "another-fixture", current); got.Code != 400 {
			t.Fatalf("wrong-password attempt %d: %d", i, got.Code)
		}
	}
	if got := change("wrong", "another-fixture", current); got.Code != 429 {
		t.Fatal("password change rate limit absent")
	}
}
func TestPasswordChangeBrowser(t *testing.T) {
	if os.Getenv("MELDMAIL_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set MELDMAIL_PLAYWRIGHT_MODULE for browser acceptance")
	}
	_, handler := passwordFixture(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	defer server.CloseClientConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	script := `
const {chromium}=require(process.env.MELDMAIL_PLAYWRIGHT_MODULE);
(async()=>{const browser=await chromium.launch({executablePath:process.env.MELDMAIL_CHROME||'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});try{
 const page=await browser.newPage();page.setDefaultTimeout(6000);await page.goto(process.env.MELDMAIL_BROWSER_URL);await page.getByLabel('管理员密码',{exact:true}).fill('original-fixture');await page.getByRole('button',{name:'登录工作台 →',exact:true}).click();await page.getByRole('button',{name:'修改登录密码',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'修改登录密码',exact:true});await dialog.getByLabel('当前登录密码',{exact:true}).fill('wrong');await dialog.getByLabel('新登录密码',{exact:true}).fill('new-fixture');await dialog.getByLabel('确认新密码',{exact:true}).fill('new-fixture');await dialog.getByRole('button',{name:'保存新密码',exact:true}).click();await dialog.getByText('当前密码不正确',{exact:true}).waitFor();
 await dialog.getByLabel('当前登录密码',{exact:true}).fill('original-fixture');await dialog.getByLabel('确认新密码',{exact:true}).fill('mismatch');await dialog.getByRole('button',{name:'保存新密码',exact:true}).click();await dialog.getByText('两次输入的新密码不一致',{exact:true}).waitFor();
 await page.setViewportSize({width:390,height:844});if(await dialog.evaluate(el=>el.scrollWidth>el.clientWidth))throw Error('mobile overflow');await dialog.getByLabel('确认新密码',{exact:true}).fill('new-fixture');await dialog.getByRole('button',{name:'保存新密码',exact:true}).click();await page.getByText('登录密码已修改，请使用新密码重新登录。',{exact:true}).waitFor();
 await page.getByLabel('管理员密码',{exact:true}).fill('new-fixture');await page.getByRole('button',{name:'登录工作台 →',exact:true}).click();await page.getByLabel('打开导航',{exact:true}).click();await page.getByRole('button',{name:'修改登录密码',exact:true}).waitFor();console.log('PASS: wrong current/mismatched confirmation, mobile password change, forced logout, new-password login');
}finally{await browser.close()}})().catch(e=>{console.error(e.message);process.exit(1)});`
	path := filepath.Join(t.TempDir(), "password-browser.cjs")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", path)
	cmd.Env = append(os.Environ(), "MELDMAIL_BROWSER_URL="+server.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("password browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}

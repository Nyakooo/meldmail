package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatsRequiresSessionAndOmitsPaths(t *testing.T) {
	_, handler := passwordFixture(t)
	request := httptest.NewRequest("GET", "/api/stats", nil)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, request)
	if unauthorized.Code != 401 {
		t.Fatal("unauthenticated statistics exposed")
	}
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"original-fixture"}`)))
	request = httptest.NewRequest("GET", "/api/stats", nil)
	request.AddCookie(login.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["mail"] == nil || data["resources"] == nil {
		t.Fatal("missing stats sections")
	}
	for _, private := range []string{"password.db", "original-fixture", "credentials_enc", "MELDMAIL_MASTER_KEY"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatal("private configuration exposed")
		}
	}
}

package site

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/nathanxiangang-web/drive/internal/store"
)

func TestAdminAndReaderPermissions(t *testing.T) {
	dsn := os.Getenv("DRIVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("supply a disposable PostgreSQL database")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const pass = "fixture-password-only"
	if err := db.ProvisionAdmin(ctx, "fixture-admin", pass); err != nil {
		t.Fatal(err)
	}
	adminToken, err := db.Authenticate(ctx, "fixture-admin", pass)
	if err != nil {
		t.Fatal(err)
	}
	s := Site{Store: db, Origin: "https://drive.example.test", DownloadEnabled: true, OpenRegistration: true}
	router := s.Routes()
	call := func(method, route, token, body string, expected int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, route, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", s.Origin)
		if token != "" {
			request.Header.Set("Cookie", cookieName+"="+token)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, request)
		if out.Code != expected {
			t.Fatalf("%s %s HTTP %d, expected %d", method, route, out.Code, expected)
		}
		return out
	}
	call("GET", "/api/admin/users", "", "", 401)
	call("POST", "/api/register", "", `{"login":"1234","password":"123456"}`, 400)
	call("POST", "/api/register", "", `{"login":"中文","password":"ab12+!"}`, 400)
	newAccount := call("POST", "/api/register", "", `{"login":"8","password":"ab12+!"}`, 204)
	cookies := newAccount.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("registration must issue a secure session")
	}
	call("POST", "/api/tasks", cookies[0].Value, `{"source":"not-a-url","operation_key":"fixture-register-task-0001"}`, 400)
	call("GET", "/api/admin/users", cookies[0].Value, "", 403)

	call("POST", "/api/admin/users", adminToken, `{"login":"fixture-reader","password":"fixture-password-only","role":"reader"}`, 204)
	readerToken, err := db.Authenticate(ctx, "fixture-reader", pass)
	if err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/admin/users", readerToken, "", 403)
	call("PUT", "/api/admin/openlist", readerToken, "{}", 403)
	call("POST", "/api/tasks", readerToken, "{}", 403)
	call("PATCH", "/api/admin/users/fixture-admin", adminToken, `{"role":"reader","enabled":false}`, 409)
	users := call("GET", "/api/admin/users", adminToken, "", 200)
	var response []store.UserDetails
	if json.Unmarshal(users.Body.Bytes(), &response) != nil || len(response) < 2 || strings.Contains(users.Body.String(), "password") {
		t.Fatal("account list disclosed invalid fields")
	}
	call("PATCH", "/api/admin/users/fixture-reader", adminToken, `{"role":"reader","enabled":false}`, 204)
	call("GET", "/api/tasks", readerToken, "", 401)
	if err := db.ReplaceAdmin(ctx, "fixture-admin", "admin", "fixture-next-password"); err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/admin/users", adminToken, "", 401)
	if _, err := db.Authenticate(ctx, "fixture-admin", pass); err == nil {
		t.Fatal("old admin could still log in")
	}
	adminToken, err = db.Authenticate(ctx, "admin", "fixture-next-password")
	if err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/admin/users", adminToken, "", 200)

}

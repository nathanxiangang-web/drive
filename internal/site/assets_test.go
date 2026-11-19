package site

import (
	"compress/gzip"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestBrowserCacheOnlyForPublicShellAndStaticAssets(t *testing.T) {
	handler := (&Site{OpenRegistration: true}).Routes()
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", "/", nil))
	if page.Code != 200 || page.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(page.Body.String(), `data-registration="true"`) {
		t.Fatal("login shell should revalidate and enable registration")
	}
	resource := regexp.MustCompile(`/assets/[0-9a-f]+/app\.js`).FindString(page.Body.String())
	if resource == "" {
		t.Fatal("asset URL missing content fingerprint")
	}
	r := httptest.NewRequest("GET", resource, nil)
	r.Header.Set("Accept-Encoding", "gzip")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, r)
	if first.Code != 200 || first.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(first.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("asset should be compressed and immutable")
	}
	gz, err := gzip.NewReader(first.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	if body, err := io.ReadAll(gz); err != nil || !strings.Contains(string(body), "showLogin") {
		t.Fatal("invalid asset response")
	}
	r.Header.Set("If-None-Match", first.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	handler.ServeHTTP(cached, r)
	if cached.Code != 304 || cached.Body.Len() != 0 {
		t.Fatal("matching ETag should prevent transfer")
	}
	auth := httptest.NewRecorder()
	handler.ServeHTTP(auth, httptest.NewRequest("GET", "/api/files", nil))
	if auth.Code != 401 || auth.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private API should require session and avoid caching")
	}
}

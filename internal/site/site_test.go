package site

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/store"
)

func TestBrowsingScopeRejectsPrivatePathsAndParentTraversal(t *testing.T) {
	s := Site{PublicRoot: "/CloudSite", PrivateRoot: "/CloudSite/DriveSpaces"}
	ctx := context.Background()
	for _, p := range []string{"DriveSpaces/user-a", "drivespaces", "云下载/old", "../DriveSpaces", "/CloudSite", "a//b", "a\\b"} {
		if _, err := s.directory(ctx, store.Account{}, "public", p); !errors.Is(err, cloud.Missing) {
			t.Errorf("accepted %q, err %v", p, err)
		}
	}
	if got, err := s.directory(ctx, store.Account{}, "public", "影视/示例"); err != nil || got != "/CloudSite/影视/示例" {
		t.Fatalf("public path=%q %v", got, err)
	}
	if _, err := s.directory(ctx, store.Account{}, "private", ""); !errors.Is(err, cloud.Missing) {
		t.Fatal("unprovisioned private folder exposed")
	}
}

func TestProtectedRoutesAndOrigin(t *testing.T) {
	s := Site{Origin: "https://drive.example.test"}
	h := s.Routes()
	for _, route := range []string{"/api/session", "/api/files", "/api/tasks", "/api/file"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", route, nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("unauthorized route %s: %d", route, response.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://outside.example.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("wrong origin HTTP %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "CloudDrive") {
		t.Fatalf("login shell HTTP %d", w.Code)
	}
}

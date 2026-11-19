package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryConnectionStore struct{ sealed []byte }

func (m *memoryConnectionStore) LoadOpenList(context.Context) ([]byte, error) { return m.sealed, nil }
func (m *memoryConnectionStore) SaveOpenList(_ context.Context, _ string, value []byte) error {
	m.sealed = bytes.Clone(value)
	return nil
}

func TestConfigureOpenListBeforeReplacingWorkingConnection(t *testing.T) {
	ctx := context.Background()
	const password = "fixture-password-only"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			var body OpenListCredentials
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Username != "scoped" || body.Password != password {
				_, _ = w.Write([]byte(`{"code":401}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"token":"mock-token"}}`))
		case "/api/fs/list":
			var body struct{ Path string }
			if json.NewDecoder(r.Body).Decode(&body) != nil || r.Header.Get("Authorization") != "mock-token" || body.Path != "/CloudSite" {
				_, _ = w.Write([]byte(`{"code":403}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":200}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	files := t.TempDir()
	keyFile := filepath.Join(files, "key")
	legacyFile := filepath.Join(files, "legacy")
	if err := os.WriteFile(keyFile, []byte(strings.Repeat("1f", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyFile, []byte("existing-token"), 0600); err != nil {
		t.Fatal(err)
	}
	db := &memoryConnectionStore{}
	c, err := OpenConnection(ctx, db, server.URL, legacyFile, "/", keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Configure(ctx, "admin", OpenListCredentials{"scoped", "incorrect", "/CloudSite"}); err == nil {
		t.Fatal("wrong password accepted")
	}
	if err := c.Configure(ctx, "admin", OpenListCredentials{"scoped", password, "/other"}); err == nil {
		t.Fatal("unreadable root accepted")
	}
	if token, _, _ := c.Token(ctx); token != "existing-token" || len(db.sealed) != 0 {
		t.Fatal("failed validations changed the working connection")
	}
	if err := c.Configure(ctx, "admin", OpenListCredentials{"scoped", password, "/CloudSite"}); err != nil {
		t.Fatal(err)
	}
	if len(db.sealed) == 0 || bytes.Contains(db.sealed, []byte(password)) {
		t.Fatal("password must be encrypted at rest")
	}
	restored, err := OpenConnection(ctx, db, server.URL, legacyFile, "/", keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if token, root, err := restored.Token(ctx); err != nil || token != "mock-token" || root != "/CloudSite" {
		t.Fatalf("configured account was not restored: %v", err)
	}
}

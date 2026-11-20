package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestShareOwnershipAndLifetime(t *testing.T) {
	dsn := os.Getenv("DRIVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("supply a disposable PostgreSQL database")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	login := "share" + Random()[:10]
	if _, err := db.Register(ctx, login, "ShareFixture6!"); err != nil {
		t.Fatal(err)
	}
	token, err := db.Authenticate(ctx, login, "ShareFixture6!")
	if err != nil {
		t.Fatal(err)
	}
	account, err := db.Account(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	file := Share{Owner: account.ID, Area: "private", Parent: "42", FileID: "456", Name: "fixture.txt", Size: 1024}
	shared, err := db.CreateShare(ctx, file, 7)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.PublicShare(ctx, shared.Token)
	if err != nil || result.Owner != account.ID || result.FileID != file.FileID || result.Size != 1024 {
		t.Fatalf("active share lookup: %+v %v", result, err)
	}
	if rows, err := db.Shares(ctx, "other", file.FileID); err != nil || len(rows) != 0 {
		t.Fatal("other owner can see a share")
	}
	if err := db.RevokeShare(ctx, "other", shared.Token); !errors.Is(err, ShareMissing) {
		t.Fatal("other owner can revoke a share")
	}
	if err := db.RevokeShare(ctx, account.ID, shared.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PublicShare(ctx, shared.Token); !errors.Is(err, ShareMissing) {
		t.Fatal("revoked share still opens")
	}
	expired, err := db.CreateShare(ctx, file, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(ctx, "UPDATE drive_shares SET expires_at=now() WHERE token=$1", expired.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PublicShare(ctx, expired.Token); !errors.Is(err, ShareMissing) {
		t.Fatal("expired share still opens")
	}
	disabled, err := db.CreateShare(ctx, file, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(ctx, "UPDATE drive_accounts SET enabled=false WHERE id=$1", account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PublicShare(ctx, disabled.Token); !errors.Is(err, ShareMissing) {
		t.Fatal("disabled owner's share still opens")
	}
}

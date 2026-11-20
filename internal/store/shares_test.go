package store

import (
	"context"
	"errors"
	"os"
	"strings"
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
	file := Share{Owner: account.ID, Area: "private", Parent: "42", FileID: "456", Name: "fixture.txt", Size: 1024, Code: "A6z9"}
	shared, err := db.CreateShare(ctx, file, 7)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.PublicShare(ctx, shared.Token)
	if err != nil || result.Owner != account.ID || result.FileID != file.FileID || result.Size != 1024 ||
		result.Code != "" || result.CodeVersion != 1 || len(result.Token) != 16 {
		t.Fatalf("active share lookup: %+v %v", result, err)
	}
	if rows, err := db.Shares(ctx, "other", file.FileID, 50, 0); err != nil || len(rows) != 0 {
		t.Fatal("other owner can see a share")
	}
	if err := db.RevokeShare(ctx, "other", shared.Token); !errors.Is(err, ShareMissing) {
		t.Fatal("other owner can revoke a share")
	}
	if _, err := db.AdminShareCode(ctx, account.ID, shared.Token, "0000"); !errors.Is(err, ShareMissing) {
		t.Fatal("member can change a share code with administrative permissions")
	}
	if _, err := db.UnlockShare(ctx, shared.Token, "a6z9"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxWrongShareCodes; i++ {
		if _, err := db.UnlockShare(ctx, shared.Token, "1234"); !errors.Is(err, WrongShareCode) {
			t.Fatal("wrong code must be refused")
		}
	}
	if _, err := db.UnlockShare(ctx, shared.Token, "A6Z9"); !errors.Is(err, ShareCodeLimited) {
		t.Fatal("rate limit did not apply after failed attempts")
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

func TestShareCodeFormat(t *testing.T) {
	for _, input := range []string{"0000", "abcd", "a9b8"} {
		if _, ok := ShareCode(input); !ok {
			t.Errorf("valid code rejected %q", input)
		}
	}
	for _, input := range []string{" 123", "é123", "!123", "123", "12345"} {
		if _, ok := ShareCode(input); ok {
			t.Errorf("invalid code accepted %q", input)
		}
	}
	for i := 0; i < 32; i++ {
		code := RandomShareCode()
		if _, ok := ShareCode(code); !ok || !strings.ContainsAny(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") || !strings.ContainsAny(code, "0123456789") {
			t.Fatalf("invalid generated code %q", code)
		}
	}
}

func TestLegacyShareKeepsPublicAddressWhenCodeChanges(t *testing.T) {
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
	if err := db.ProvisionAdmin(ctx, "admin", "fixture-next-password"); err != nil {
		t.Fatal(err)
	}
	session, err := db.Authenticate(ctx, "admin", "fixture-next-password")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := db.Account(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	legacy := Random()
	if _, err := db.db.Exec(ctx, `INSERT INTO drive_shares
(token,owner,area,dir,parent_id,file_id,file_name,file_size,expires_at)
VALUES ($1,$2,'public','','42','777','legacy fixture',1024,now()+interval '1 day')`, legacy, admin.ID); err != nil {
		t.Fatal(err)
	}
	if share, err := db.PublicShare(ctx, legacy); err != nil || share.CodeVersion != 0 {
		t.Fatal("original address lost before setting a code")
	}
	first, err := db.AdminShareCode(ctx, admin.ID, legacy, "A123")
	if err != nil || first.Token != legacy {
		t.Fatalf("setting a code changed the public address: %v", err)
	}
	var key string
	err = db.db.QueryRow(ctx, "SELECT token FROM drive_shares WHERE public_token=$1", legacy).Scan(&key)
	if err != nil || key == legacy {
		t.Fatal("an older version could bypass the code on this link")
	}
	if _, err := db.PublicShare(ctx, key); !errors.Is(err, ShareMissing) {
		t.Fatal("the internal token unexpectedly became a second address")
	}
	oldGrant, err := db.UnlockShare(ctx, legacy, "A123")
	if err != nil || oldGrant != 1 {
		t.Fatalf("the new code did not unlock the original address: %v", err)
	}
	second, err := db.AdminShareCode(ctx, admin.ID, legacy, "B456")
	if err != nil || second.Token != legacy || !second.Expires.Equal(first.Expires) {
		t.Fatalf("changing the code affected the address or expiration: %v", err)
	}
	if _, err := db.UnlockShare(ctx, legacy, "A123"); !errors.Is(err, WrongShareCode) {
		t.Fatal("the old code still unlocks the original link")
	}
	if grant, err := db.UnlockShare(ctx, legacy, "B456"); err != nil || grant <= oldGrant {
		t.Fatal("the new code did not create a new verification session")
	}
	listed, _, err := db.AdminShares(ctx, admin.ID, "legacy fixture", 0)
	if err != nil || len(listed) != 1 || listed[0].Token != legacy || listed[0].Code != "B456" {
		t.Fatal("admin list does not display the original address and current code")
	}
	shares, err := db.Shares(ctx, admin.ID, "", 50, 0)
	if err != nil || len(shares) != 1 || shares[0].Token != legacy {
		t.Fatal("share history does not display the original address")
	}
	if err := db.RevokeShare(ctx, admin.ID, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PublicShare(ctx, legacy); !errors.Is(err, ShareMissing) {
		t.Fatal("the original link remains accessible after revocation")
	}
}

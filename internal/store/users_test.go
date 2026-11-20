package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestUserManagementPreservesCloudOwnership(t *testing.T) {
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

	const adminLogin = "admin"
	if err := db.ProvisionAdmin(ctx, adminLogin, "fixture-next-password"); err != nil {
		t.Fatal(err)
	}
	adminToken, err := db.Authenticate(ctx, adminLogin, "fixture-next-password")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := db.Account(ctx, adminToken)
	if err != nil {
		t.Fatal(err)
	}
	oldLogin := "fixture" + Random()[:8]
	newLogin := oldLogin + "new"
	if err := db.CreateUser(ctx, admin.ID, oldLogin, "Mix4!X", Member); err != nil {
		t.Fatal(err)
	}
	token, err := db.Authenticate(ctx, oldLogin, "Mix4!X")
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.Account(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.BindNamedFolder(ctx, old.ID, old.CloudFolderName(), "65775"); err != nil {
		t.Fatal(err)
	}
	source := "https://example.com/test.txt"
	if _, err := db.Submit(ctx, old.ID, Random(), source, source); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if err := db.ChangeUser(ctx, admin, oldLogin, UserChange{Login: &newLogin, Password: "Mix6!X", Role: Reader, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Account(ctx, token); err == nil {
		t.Fatal("stale session accepted")
	}
	if _, err := db.Authenticate(ctx, newLogin, "Mix4!X"); err == nil {
		t.Fatal("old password accepted")
	}
	token, err = db.Authenticate(ctx, newLogin, "Mix6!X")
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := db.Account(ctx, token)
	if err != nil || renamed.ID != old.ID || renamed.CloudFolderName() != oldLogin || renamed.NamedFolderID != "65775" || renamed.CanDownload() {
		t.Fatal("rename changed storage identity or failed to apply permission")
	}
	if _, err := db.Register(ctx, oldLogin, "Mix6!X"); !errors.Is(err, UserExists) {
		t.Fatal("another account claimed an existing cloud path")
	}
	if err := db.DeleteUser(ctx, admin, admin.Login); !errors.Is(err, ProtectedAdmin) {
		t.Fatal("administrator deleted")
	}
	if err := db.DeleteUser(ctx, admin, newLogin); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Account(ctx, token); err == nil {
		t.Fatal("deleted account can still sign in")
	}
	if got, err := db.Users(ctx, newLogin); err != nil || len(got) != 0 {
		t.Fatal("deleted account appeared in the admin list")
	}
	binding, err := db.ResultOwner(ctx, old.ID)
	if err != nil || binding.NamedFolderID != "65775" || binding.CloudFolderName() != oldLogin {
		t.Fatal("account deletion altered cloud ownership")
	}
	if got, err := db.ListJobs(ctx, old.ID); err != nil || len(got) != 1 {
		t.Fatal("account deletion removed download history")
	}
}

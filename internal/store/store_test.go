package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestJobIdempotencyAndRecovery(t *testing.T) {
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

	account := func() Account {
		t.Helper()
		login := "test-" + Random()[:16]
		password := Random()
		if err := db.Provision(ctx, login, []byte(password)); err != nil {
			t.Fatal(err)
		}
		token, err := db.Authenticate(ctx, login, password)
		if err != nil {
			t.Fatal(err)
		}
		a, err := db.Account(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	first, second := account(), account()
	key := Random()
	source := "https://example.com/test.txt"
	initial, err := db.Submit(ctx, first.ID, key, source, source)
	if err != nil {
		t.Fatal(err)
	}
	again, err := db.Submit(ctx, first.ID, key, source, source)
	if err != nil || again.ID != initial.ID {
		t.Fatalf("same operation must return the same task: %v", err)
	}
	if _, err := db.Submit(ctx, first.ID, key, source+"x", source+"x"); !errors.Is(err, Conflict) {
		t.Fatalf("operation key reuse must fail: %v", err)
	}
	if _, err := db.Submit(ctx, first.ID, Random(), source, source); !errors.Is(err, Active) {
		t.Fatalf("a fresh operation cannot repeat an active source: %v", err)
	}
	if _, err := db.Submit(ctx, second.ID, Random(), source, source); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.Claim(ctx)
	if err != nil || claimed.ID != initial.ID {
		t.Fatalf("first task must be claimed first: %v", err)
	}
	if err := db.RecordTarget(ctx, claimed.ID, "1234", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := db.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListJobs(ctx, first.ID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "UNCERTAIN" || jobs[0].Source != "" || jobs[0].DirectoryID != "1234" {
		t.Fatalf("recovery must preserve the target and user scope: %v", err)
	}
	next, err := db.Claim(ctx)
	if err != nil || next.Owner != second.ID {
		t.Fatalf("uncertain task must never be claimed again: %v", err)
	}
}

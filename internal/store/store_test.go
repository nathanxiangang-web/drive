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
	if err := db.BindNamedFolder(ctx, first.ID, first.Login, "1234"); err != nil {
		t.Fatal(err)
	}
	if err := db.BindNamedFolder(ctx, second.ID, second.Login, "1234"); err == nil {
		t.Fatal("a named cloud directory cannot be bound to two accounts")
	}

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
	repeat, err := db.Submit(ctx, first.ID, Random(), source, source)
	if err != nil || repeat.ID == initial.ID {
		t.Fatalf("a new explicit submission must be accepted: %v", err)
	}
	if ok, err := db.HideTask(ctx, second.ID, initial.ID); err != nil || ok {
		t.Fatal("another account hid the task")
	}
	if ok, err := db.HideTask(ctx, first.ID, repeat.ID); err != nil || !ok {
		t.Fatal("owner could not hide the task")
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
	if err := db.ObserveTask(ctx, claimed.ID, "LISTED", 99.5); err != nil {
		t.Fatal(err)
	}
	if err := db.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListJobs(ctx, first.ID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "UNCERTAIN" || jobs[0].Source != "" || jobs[0].DirectoryID != "1234" || !jobs[0].NamedDestination || jobs[0].Progress != 99.5 || jobs[0].ProviderVisible != "LISTED" {
		t.Fatalf("recovery must preserve the target and user scope: %v", err)
	}
	next, err := db.Claim(ctx)
	if err != nil || next.ID != repeat.ID {
		t.Fatalf("clearing the view must not prevent dispatch: %v", err)
	}
	if err := db.HideTasks(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if listed, err := db.ListJobs(ctx, first.ID); err != nil || len(listed) != 0 {
		t.Fatal("owner's list was not cleared")
	}
	if listed, err := db.ListJobs(ctx, second.ID); err != nil || len(listed) != 1 {
		t.Fatal("clearing one account affected another account")
	}
	active, err := db.ActiveJobs(ctx)
	if err != nil || len(active) != 1 || active[0].ID != initial.ID {
		t.Fatal("hidden transfer is still eligible for reconciliation")
	}
}

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var Invalid = errors.New("账号或密码无效")
var Conflict = errors.New("操作编号已被不同链接使用")
var loginPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type Store struct {
	db    *pgxpool.Pool
	dummy []byte
}

type Account struct {
	ID            string `json:"-"`
	Login         string `json:"login"`
	Role          string `json:"role"`
	FolderID      string `json:"-"`
	NamedFolderID string `json:"-"`
}

func (a Account) FolderName() string { return "user-" + a.ID }

func Random() string { var b [32]byte; rand.Read(b[:]); return hex.EncodeToString(b[:]) }

const schema = `
CREATE TABLE IF NOT EXISTS drive_accounts (
  id text PRIMARY KEY, login text NOT NULL UNIQUE, password_hash bytea NOT NULL,
  folder_id text NOT NULL DEFAULT '', enabled boolean NOT NULL DEFAULT true);

ALTER TABLE drive_accounts ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'member'
  CHECK(role IN ('admin','member','reader'));
ALTER TABLE drive_accounts ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE drive_accounts ADD COLUMN IF NOT EXISTS named_folder_id text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS drive_named_folder ON drive_accounts(named_folder_id) WHERE named_folder_id<>'';
CREATE TABLE IF NOT EXISTS drive_settings(name text PRIMARY KEY,sealed bytea NOT NULL);
CREATE TABLE IF NOT EXISTS drive_sessions (
  token_hash bytea PRIMARY KEY, account_id text NOT NULL REFERENCES drive_accounts(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS drive_jobs (
  id text PRIMARY KEY, owner text NOT NULL REFERENCES drive_accounts(id), operation_key text NOT NULL,
  source_hash bytea NOT NULL, source text NOT NULL, provider_hash text NOT NULL DEFAULT '',
  directory_id text NOT NULL DEFAULT '', name text NOT NULL DEFAULT '',
  status text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(owner,operation_key));
ALTER TABLE drive_jobs ADD COLUMN IF NOT EXISTS named_destination boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS drive_jobs_pending ON drive_jobs(status,created_at);
CREATE INDEX IF NOT EXISTS drive_jobs_owner ON drive_jobs(owner,created_at DESC);
DROP INDEX IF EXISTS drive_jobs_active_source;
ALTER TABLE drive_jobs ADD COLUMN IF NOT EXISTS hidden_at timestamptz;
ALTER TABLE drive_jobs ADD COLUMN IF NOT EXISTS provider_progress real NOT NULL DEFAULT -1;
ALTER TABLE drive_jobs ADD COLUMN IF NOT EXISTS provider_visible text NOT NULL DEFAULT '';`

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(ctx, schema); err != nil {
		db.Close()
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte(Random()), bcrypt.DefaultCost)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db, dummy}, nil
}

func (s *Store) Close()                         { s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

func (s *Store) Provision(ctx context.Context, login string, password []byte) error {
	if !loginPattern.MatchString(login) || len(password) < 12 || len(password) > 72 {
		return Invalid
	}
	hash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO drive_accounts(id,login,password_hash) VALUES ($1,$2,$3) ON CONFLICT (login) DO NOTHING`, Random()[:32], login, hash)
	return err
}

func (s *Store) Authenticate(ctx context.Context, login, password string) (string, error) {
	if !loginPattern.MatchString(login) || len(password) > 72 {
		return "", Invalid
	}
	var account string
	var hash []byte
	err := s.db.QueryRow(ctx, `SELECT id,password_hash FROM drive_accounts WHERE login=$1 AND enabled`, login).Scan(&account, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		hash = s.dummy
	}
	match := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if err != nil || !match {
		return "", Invalid
	}
	return issueSession(ctx, s.db, account)
}

type sessionWriter interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func issueSession(ctx context.Context, db sessionWriter, account string) (string, error) {
	token := Random()
	digest := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO drive_sessions VALUES($1,$2,now()+interval '12 hours')`, digest[:], account); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) Account(ctx context.Context, token string) (Account, error) {
	var account Account
	if len(token) != 64 {
		return account, Invalid
	}
	digest := sha256.Sum256([]byte(token))
	err := s.db.QueryRow(ctx, `SELECT a.id,a.login,a.folder_id,a.role,a.named_folder_id FROM drive_accounts a JOIN drive_sessions t ON a.id=t.account_id WHERE t.token_hash=$1 AND t.expires_at>now() AND a.enabled`, digest[:]).Scan(&account.ID, &account.Login, &account.FolderID, &account.Role, &account.NamedFolderID)
	return account, err
}

func (s *Store) Logout(ctx context.Context, token string) error {
	digest := sha256.Sum256([]byte(token))
	_, err := s.db.Exec(ctx, `DELETE FROM drive_sessions WHERE token_hash=$1`, digest[:])
	return err
}

func (s *Store) BindNamedFolder(ctx context.Context, owner, login, folder string) error {
	tag, err := s.db.Exec(ctx, `UPDATE drive_accounts SET named_folder_id=$3 WHERE id=$1 AND login=$2 AND enabled AND (named_folder_id='' OR named_folder_id=$3)`, owner, login, folder)
	if err == nil && tag.RowsAffected() != 1 {
		return Conflict
	}
	return err
}

type Job struct {
	ID               string    `json:"id"`
	Owner            string    `json:"-"`
	Source           string    `json:"-"`
	ProviderHash     string    `json:"-"`
	DirectoryID      string    `json:"-"`
	NamedDestination bool      `json:"-"`
	Progress         float64   `json:"progress"`
	ProviderVisible  string    `json:"provider_visible,omitempty"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	Created          time.Time `json:"created"`
}

const jobCols = `id,owner,source,provider_hash,directory_id,name,status,created_at,named_destination,provider_progress,provider_visible`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Owner, &j.Source, &j.ProviderHash, &j.DirectoryID, &j.Name, &j.Status, &j.Created, &j.NamedDestination, &j.Progress, &j.ProviderVisible)
	return j, err
}

func (s *Store) Submit(ctx context.Context, owner, key, identity, execution string) (Job, error) {
	digest := sha256.Sum256([]byte(identity))
	j, err := scanJob(s.db.QueryRow(ctx, `INSERT INTO drive_jobs(id,owner,operation_key,source_hash,source,status) SELECT $1,$2,$3,$4,$5,'QUEUED' FROM drive_accounts WHERE id=$2 AND enabled AND role IN ('admin','member')
ON CONFLICT(owner,operation_key) DO UPDATE SET operation_key=EXCLUDED.operation_key WHERE drive_jobs.source_hash=EXCLUDED.source_hash RETURNING `+jobCols, Random()[:32], owner, key, digest[:], execution))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, Conflict
	}
	return j, err
}

func (s *Store) ListJobs(ctx context.Context, owner string) ([]Job, error) {
	rows, err := s.db.Query(ctx, `SELECT `+jobCols+` FROM drive_jobs WHERE owner=$1 AND hidden_at IS NULL ORDER BY created_at DESC LIMIT 100`, owner)
	return collect(rows, err)
}

func (s *Store) ActiveJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.Query(ctx, `SELECT `+jobCols+` FROM drive_jobs WHERE status IN ('RUNNING','VERIFYING','UNCERTAIN') AND directory_id<>'' ORDER BY updated_at LIMIT 50`)
	return collect(rows, err)
}

func collect(rows pgx.Rows, err error) ([]Job, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *Store) Claim(ctx context.Context) (Job, error) {
	return scanJob(s.db.QueryRow(ctx, `UPDATE drive_jobs SET status='DISPATCHING',updated_at=now() WHERE id=(SELECT id FROM drive_jobs WHERE status='QUEUED' ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING `+jobCols))
}

func (s *Store) Recover(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE drive_jobs SET status='UNCERTAIN',source='',updated_at=now() WHERE status='DISPATCHING'`)
	return err
}

func (s *Store) RecordTarget(ctx context.Context, job, directory, expectedHash string) error {
	tag, err := s.db.Exec(ctx, `UPDATE drive_jobs SET directory_id=$2,provider_hash=$3,named_destination=true WHERE id=$1 AND status='DISPATCHING'`, job, directory, expectedHash)
	if err == nil && tag.RowsAffected() != 1 {
		return Conflict
	}
	return err
}

func (s *Store) Update(ctx context.Context, job, status, providerHash, name string) error {
	_, err := s.db.Exec(ctx, `UPDATE drive_jobs SET status=$2,provider_hash=COALESCE(NULLIF($3,''),provider_hash),name=COALESCE(NULLIF($4,''),name),source='',updated_at=now() WHERE id=$1`, job, status, providerHash, name)
	return err
}

func (s *Store) User(ctx context.Context, id string) (Account, error) {
	var account Account
	err := s.db.QueryRow(ctx, `SELECT id,login,folder_id,role,named_folder_id FROM drive_accounts WHERE id=$1 AND enabled`, id).Scan(&account.ID, &account.Login, &account.FolderID, &account.Role, &account.NamedFolderID)
	return account, err
}

type LegacyTarget struct{ Name, ID string }

func (s *Store) LegacyTargets(ctx context.Context, owner string) ([]LegacyTarget, error) {
	rows, err := s.db.Query(ctx, `SELECT 'task-'||left(id,12), directory_id FROM drive_jobs
WHERE owner=$1 AND NOT named_destination AND directory_id<>'' ORDER BY created_at,id`, owner)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[LegacyTarget])
}

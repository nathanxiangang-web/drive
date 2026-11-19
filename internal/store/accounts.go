package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

const (
	Admin  = "admin"
	Member = "member"
	Reader = "reader"
)

var (
	Forbidden      = errors.New("没有管理权限")
	UserExists     = errors.New("账号已存在")
	UserMissing    = errors.New("账号不存在")
	ProtectedAdmin = errors.New("管理员账号受保护")
	InvalidAccount = errors.New("账号需使用 3–64 个字母、数字或 ._-，密码需为 12–72 字节")
)

func validRole(role string) bool {
	return role == Admin || role == Member || role == Reader
}

func (a Account) CanDownload() bool {
	return a.Role == Admin || a.Role == Member
}

type UserDetails struct {
	Login   string    `json:"login"`
	Role    string    `json:"role"`
	Enabled bool      `json:"enabled"`
	Created time.Time `json:"created"`
}

func accountHash(login, password string) ([]byte, error) {
	if !loginPattern.MatchString(login) || len(password) < 12 || len(password) > 72 {
		return nil, InvalidAccount
	}
	return bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
}

// ProvisionAdmin upgrades a pre-existing install only when the supplied
// bootstrap password also authenticates the selected account.
func (s *Store) ProvisionAdmin(ctx context.Context, login, password string) error {
	hash, err := accountHash(login, password)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `LOCK TABLE drive_accounts IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var existing []byte
	var role string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT password_hash,role,enabled FROM drive_accounts WHERE login=$1`, login).Scan(&existing, &role, &enabled)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if found && (!enabled || bcrypt.CompareHashAndPassword(existing, []byte(password)) != nil) {
		return Forbidden
	}
	if found && role == Admin {
		return tx.Commit(ctx)
	}
	var ready bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM drive_accounts WHERE role='admin' AND enabled)`).Scan(&ready); err != nil {
		return err
	}
	if ready {
		return Forbidden
	}
	if found {
		_, err = tx.Exec(ctx, `UPDATE drive_accounts SET role='admin' WHERE login=$1`, login)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO drive_accounts(id,login,storage_name,password_hash,role) VALUES($1,$2,$2,$3,'admin')`, Random()[:32], login, hash)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Users(ctx context.Context, query string) ([]UserDetails, error) {
	rows, err := s.db.Query(ctx, `SELECT login,role,enabled,created_at FROM drive_accounts WHERE deleted_at IS NULL AND strpos(lower(login),lower($1)) > 0 ORDER BY login`, query)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[UserDetails])
}

func (s *Store) CreateUser(ctx context.Context, actor, login, password, role string) error {
	if !validRole(role) {
		return InvalidAccount
	}
	hash, err := accountHash(login, password)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO drive_accounts(id,login,storage_name,password_hash,role)
SELECT $1,$2,$2,$3,$4 FROM drive_accounts WHERE id=$5 AND role='admin' AND enabled`, Random()[:32], login, hash, role, actor)
	var violation *pgconn.PgError
	if errors.As(err, &violation) && violation.Code == "23505" {
		return UserExists
	}
	if err == nil && tag.RowsAffected() != 1 {
		return Forbidden
	}
	return err
}

func (s *Store) LoadOpenList(ctx context.Context) ([]byte, error) {
	var sealed []byte
	err := s.db.QueryRow(ctx, `SELECT sealed FROM drive_settings WHERE name='openlist'`).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return sealed, err
}

func (s *Store) SaveOpenList(ctx context.Context, actor string, sealed []byte) error {
	tag, err := s.db.Exec(ctx, `INSERT INTO drive_settings(name,sealed) SELECT 'openlist',$1
FROM drive_accounts WHERE id=$2 AND enabled AND role='admin'
ON CONFLICT(name) DO UPDATE SET sealed=EXCLUDED.sealed`, sealed, actor)
	if err == nil && tag.RowsAffected() != 1 {
		return Forbidden
	}
	return err
}

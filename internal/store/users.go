package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type UserChange struct {
	Login    *string
	Password string
	Role     string
	Enabled  *bool
}

func (s *Store) ChangeUser(ctx context.Context, actor Account, current string, update UserChange) error {
	if update.Enabled == nil || !validRole(update.Role) ||
		(update.Login != nil && !loginPattern.MatchString(*update.Login)) {
		return InvalidAccount
	}
	var hash []byte
	if update.Password != "" {
		if err := validateRegistrationPassword(update.Password); err != nil {
			return err
		}
		var err error
		hash, err = bcrypt.GenerateFromPassword([]byte(update.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
	}
	return s.manageUser(ctx, actor, current, func(tx pgx.Tx, id string) error {
		login := current
		if update.Login != nil {
			login = *update.Login
		}
		var reserved bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM drive_accounts
WHERE id<>$1 AND (lower(login)=lower($2) OR lower(storage_name)=lower($2)))`, id, login).Scan(&reserved)
		if err != nil {
			return err
		}
		if reserved {
			return UserExists
		}
		_, err = tx.Exec(ctx, `UPDATE drive_accounts
SET login=$2,role=$3,enabled=$4,password_hash=COALESCE($5,password_hash) WHERE id=$1`,
			id, login, update.Role, *update.Enabled, hash)
		return err
	})
}

func (s *Store) DeleteUser(ctx context.Context, actor Account, login string) error {
	return s.manageUser(ctx, actor, login, func(tx pgx.Tx, id string) error {
		_, err := tx.Exec(ctx, `UPDATE drive_accounts SET deleted_at=now(), enabled=false, password_hash=''::bytea WHERE id=$1`, id)
		return err
	})
}

// The account row remains the owner of its jobs and provider directory.
// All mutations affect the website database and its sessions only.
func (s *Store) manageUser(ctx context.Context, actor Account, login string, change func(pgx.Tx, string) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `LOCK TABLE drive_accounts IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var authorized bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM drive_accounts WHERE id=$1 AND role='admin' AND enabled)`, actor.ID).Scan(&authorized); err != nil {
		return err
	}
	if !authorized {
		return Forbidden
	}
	var id, role string
	err = tx.QueryRow(ctx, `SELECT id,role FROM drive_accounts WHERE login=$1 AND deleted_at IS NULL`, login).Scan(&id, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserMissing
	}
	if err != nil {
		return err
	}
	if role == Admin {
		return ProtectedAdmin
	}
	if err := change(tx, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM drive_sessions WHERE account_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

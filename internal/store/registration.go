package store

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

var (
	ReservedLogin               = errors.New("该用户名保留给管理员")
	InvalidRegistrationLogin    = errors.New("用户名只能使用英文字母和数字")
	InvalidRegistrationPassword = errors.New("密码至少 6 位，须同时包含英文字母和数字，可使用特殊符号")
	registrationLogin           = regexp.MustCompile(`^[a-zA-Z0-9]{1,64}$`)
)

func registrationPolicy(login, password string) error {
	if strings.EqualFold(login, "admin") {
		return ReservedLogin
	}
	if !registrationLogin.MatchString(login) {
		return InvalidRegistrationLogin
	}
	return validateRegistrationPassword(password)
}

func validateRegistrationPassword(password string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 6 || len(password) > 72 {
		return InvalidRegistrationPassword
	}
	var letter, digit bool
	for _, r := range password {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return InvalidRegistrationPassword
		}
		letter = letter || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		digit = digit || (r >= '0' && r <= '9')
	}
	if !letter || !digit {
		return InvalidRegistrationPassword
	}
	return nil
}

func (s *Store) Register(ctx context.Context, login, password string) (string, error) {
	if err := registrationPolicy(login, password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	account := Random()[:32]
	_, err = tx.Exec(ctx, `INSERT INTO drive_accounts(id,login,storage_name,password_hash,role) VALUES($1,$2,$2,$3,'member')`, account, login, hash)
	var conflict *pgconn.PgError
	if errors.As(err, &conflict) && conflict.Code == "23505" {
		return "", UserExists
	}
	if err != nil {
		return "", err
	}
	token, err := issueSession(ctx, tx, account)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}

// ReplaceAdmin is for an explicit operator-initiated account change. It
// preserves the account ID and revokes sessions created under old credentials.
func (s *Store) ReplaceAdmin(ctx context.Context, oldLogin, login, password string) error {
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
	var account string
	err = tx.QueryRow(ctx, `UPDATE drive_accounts SET login=$2,password_hash=$3
WHERE login=$1 AND role='admin' AND enabled RETURNING id`, oldLogin, login, hash).Scan(&account)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM drive_sessions WHERE account_id=$1`, account); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

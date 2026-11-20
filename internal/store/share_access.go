package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	InvalidShareCode = errors.New("提取码须为 4 位英文字母或数字")
	WrongShareCode   = errors.New("提取码不正确")
	ShareCodeLimited = errors.New("尝试过于频繁，请稍后再试")
	shareCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{4}$`)
)

func ShareCode(input string) (string, bool) {
	return strings.ToUpper(input), shareCodePattern.MatchString(input)
}

func RandomShareCode() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for {
		var data [8]byte
		rand.Read(data[:])
		code := make([]byte, 0, 4)
		letter, digit := false, false
		for _, b := range data {
			if b >= 252 {
				continue
			}
			c := alphabet[int(b)%len(alphabet)]
			code = append(code, c)
			letter = letter || c >= 'A'
			digit = digit || c <= '9'
			if len(code) == 4 {
				break
			}
		}
		if len(code) == 4 && letter && digit {
			return string(code)
		}
	}
}

const maxWrongShareCodes = 8

// Serializing attempts on this share keeps the limit effective across
// concurrent requests and server restarts, regardless of the client address.
func (s *Store) UnlockShare(ctx context.Context, token, code string) (int64, error) {
	code, ok := ShareCode(code)
	if !ok {
		return 0, InvalidShareCode
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var expected string
	var version int64
	var failed int
	err = tx.QueryRow(ctx, `SELECT s.access_code,s.code_version,
CASE WHEN s.code_window_end>now() THEN s.failed_codes ELSE 0 END
FROM drive_shares s JOIN drive_accounts a ON a.id=s.owner
WHERE s.token=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND a.enabled FOR UPDATE OF s`, token).
		Scan(&expected, &version, &failed)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ShareMissing
	}
	if err != nil {
		return 0, err
	}
	if version == 0 {
		return 0, nil
	}
	if failed >= maxWrongShareCodes {
		return 0, ShareCodeLimited
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
		return version, nil
	}
	_, err = tx.Exec(ctx, `UPDATE drive_shares SET failed_codes=$2,
code_window_end=CASE WHEN $2=1 THEN now()+interval '10 min' ELSE code_window_end END WHERE token=$1`, token, failed+1)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return 0, WrongShareCode
}

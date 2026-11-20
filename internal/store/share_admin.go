package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const AdminSharePageSize = 50

type AdminShare struct {
	Share
	Login   string    `json:"login"`
	Created time.Time `json:"created"`
	Active  bool      `json:"active"`
}

func (s *Store) AdminShares(ctx context.Context, admin, query string, offset int) ([]AdminShare, bool, error) {
	rows, err := s.db.Query(ctx, `SELECT s.token,s.file_name,s.file_size,s.expires_at,s.revoked_at,s.access_code,
a.login,s.created_at,(s.revoked_at IS NULL AND s.expires_at>now() AND a.enabled)
FROM drive_shares s JOIN drive_accounts a ON a.id=s.owner
WHERE EXISTS(SELECT 1 FROM drive_accounts WHERE id=$1 AND role='admin' AND enabled)
AND ($2='' OR strpos(lower(a.login),lower($2))>0 OR strpos(lower(s.file_name),lower($2))>0)
ORDER BY s.created_at DESC,s.token LIMIT $3 OFFSET $4`, admin, query, AdminSharePageSize+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := []AdminShare{}
	for rows.Next() {
		var item AdminShare
		if err := rows.Scan(&item.Token, &item.Name, &item.Size, &item.Expires, &item.Revoked,
			&item.Code, &item.Login, &item.Created, &item.Active); err != nil {
			return nil, false, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(result) > AdminSharePageSize {
		return result[:AdminSharePageSize], true, nil
	}
	return result, false, nil
}

func (s *Store) AdminShareCode(ctx context.Context, admin, token, input string) (Share, error) {
	code, ok := ShareCode(input)
	if !ok {
		return Share{}, InvalidShareCode
	}
	var share Share
	// Upgrade a legacy link by replacing its token. A previous binary
	// accepts 64-character tokens only, so rolling back cannot expose
	// a share that has begun requiring a code.
	err := s.db.QueryRow(ctx, `UPDATE drive_shares SET
token=CASE WHEN code_version=0 THEN $4 ELSE token END,access_code=$3,code_version=code_version+1,
failed_codes=0,code_window_end=NULL
WHERE token=$2 AND revoked_at IS NULL AND expires_at>now()
AND EXISTS(SELECT 1 FROM drive_accounts WHERE id=$1 AND role='admin' AND enabled)
RETURNING token,access_code,file_name,file_size,expires_at`, admin, token, code, shortShareToken()).
		Scan(&share.Token, &share.Code, &share.Name, &share.Size, &share.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Share{}, ShareMissing
	}
	return share, err
}

func (s *Store) AdminRevokeShare(ctx context.Context, admin, token string) error {
	tag, err := s.db.Exec(ctx, `UPDATE drive_shares SET revoked_at=COALESCE(revoked_at,now())
WHERE token=$2 AND EXISTS(SELECT 1 FROM drive_accounts WHERE id=$1 AND role='admin' AND enabled)`, admin, token)
	if err == nil && tag.RowsAffected() == 0 {
		return ShareMissing
	}
	return err
}

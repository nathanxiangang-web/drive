package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ShareMissing = errors.New("分享链接已失效")

const shareSchema = `
CREATE TABLE IF NOT EXISTS drive_shares (
  token text PRIMARY KEY, owner text NOT NULL REFERENCES drive_accounts(id),
  area text NOT NULL CHECK (area IN ('public','private')),
  dir text NOT NULL, parent_id text NOT NULL, file_id text NOT NULL,
  file_name text NOT NULL, file_size bigint NOT NULL CHECK (file_size>=0),
  expires_at timestamptz NOT NULL, revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE drive_shares ADD COLUMN IF NOT EXISTS access_code text NOT NULL DEFAULT '';
ALTER TABLE drive_shares ADD COLUMN IF NOT EXISTS code_version bigint NOT NULL DEFAULT 0;
ALTER TABLE drive_shares ADD COLUMN IF NOT EXISTS failed_codes integer NOT NULL DEFAULT 0;
ALTER TABLE drive_shares ADD COLUMN IF NOT EXISTS code_window_end timestamptz;
ALTER TABLE drive_shares ADD COLUMN IF NOT EXISTS legacy_token text NOT NULL DEFAULT '';
ALTER TABLE drive_shares ADD COLUMN IF NOT EXISTS public_token text
  GENERATED ALWAYS AS (COALESCE(NULLIF(legacy_token,''),token)) STORED;
CREATE UNIQUE INDEX IF NOT EXISTS drive_shares_public_token ON drive_shares(public_token);
CREATE INDEX IF NOT EXISTS drive_shares_by_owner ON drive_shares(owner,file_id,created_at DESC);`

type Share struct {
	Token       string     `json:"token"`
	Owner       string     `json:"-"`
	Area        string     `json:"-"`
	Dir         string     `json:"-"`
	Parent      string     `json:"-"`
	FileID      string     `json:"-"`
	Name        string     `json:"name"`
	Size        int64      `json:"size"`
	Expires     time.Time  `json:"expires"`
	Revoked     *time.Time `json:"revoked,omitempty"`
	Code        string     `json:"code,omitempty"`
	CodeVersion int64      `json:"-"`
}

func shortShareToken() string {
	var data [12]byte
	rand.Read(data[:])
	return base64.RawURLEncoding.EncodeToString(data[:])
}

func (s *Store) CreateShare(ctx context.Context, share Share, days int) (Share, error) {
	code, ok := ShareCode(share.Code)
	if !ok {
		return Share{}, InvalidShareCode
	}
	share.Token, share.Code = shortShareToken(), code
	err := s.db.QueryRow(ctx, `INSERT INTO drive_shares(token,owner,area,dir,parent_id,file_id,file_name,file_size,expires_at,access_code,code_version)
SELECT $1,id,$3,$4,$5,$6,$7,$8,now()+$9::int*interval '24 hours',$10,1 FROM drive_accounts WHERE id=$2 AND enabled
RETURNING expires_at`, share.Token, share.Owner, share.Area, share.Dir, share.Parent, share.FileID, share.Name, share.Size, days, code).Scan(&share.Expires)
	return share, err
}

func (s *Store) Shares(ctx context.Context, owner, fileID string) ([]Share, error) {
	rows, err := s.db.Query(ctx, `SELECT public_token,file_name,file_size,expires_at,revoked_at,access_code FROM drive_shares
WHERE owner=$1 AND file_id=$2 ORDER BY created_at DESC LIMIT 100`, owner, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shares := []Share{}
	for rows.Next() {
		var item Share
		if err := rows.Scan(&item.Token, &item.Name, &item.Size, &item.Expires, &item.Revoked, &item.Code); err != nil {
			return nil, err
		}
		shares = append(shares, item)
	}
	return shares, rows.Err()
}

func (s *Store) PublicShare(ctx context.Context, token string) (Share, error) {
	var share Share
	err := s.db.QueryRow(ctx, `SELECT s.public_token,s.owner,s.area,s.dir,s.parent_id,s.file_id,s.file_name,s.file_size,s.expires_at,s.code_version
FROM drive_shares s JOIN drive_accounts a ON a.id=s.owner
WHERE s.public_token=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND a.enabled`, token).
		Scan(&share.Token, &share.Owner, &share.Area, &share.Dir, &share.Parent, &share.FileID, &share.Name, &share.Size, &share.Expires, &share.CodeVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Share{}, ShareMissing
	}
	return share, err
}

func (s *Store) RevokeShare(ctx context.Context, owner, token string) error {
	tag, err := s.db.Exec(ctx, `UPDATE drive_shares SET revoked_at=COALESCE(revoked_at,now()) WHERE owner=$1 AND public_token=$2`, owner, token)
	if err == nil && tag.RowsAffected() == 0 {
		return ShareMissing
	}
	return err
}

package store

import "context"

func (s *Store) HideTask(ctx context.Context, owner, id string) (bool, error) {
	tag, err := s.db.Exec(ctx, `UPDATE drive_jobs SET hidden_at=COALESCE(hidden_at,now()) WHERE owner=$1 AND id=$2`, owner, id)
	return tag.RowsAffected() != 0, err
}

func (s *Store) HideTasks(ctx context.Context, owner string) error {
	_, err := s.db.Exec(ctx, `UPDATE drive_jobs SET hidden_at=now() WHERE owner=$1 AND hidden_at IS NULL`, owner)
	return err
}

// ResultOwner is used only to verify results of previously submitted jobs.
// Disabling sign-in does not stop an already accepted provider transfer.
func (s *Store) ResultOwner(ctx context.Context, id string) (Account, error) {
	var account Account
	err := s.db.QueryRow(ctx, `SELECT id,login,folder_id,role,named_folder_id FROM drive_accounts WHERE id=$1`, id).
		Scan(&account.ID, &account.Login, &account.FolderID, &account.Role, &account.NamedFolderID)
	return account, err
}

func (s *Store) ObserveTask(ctx context.Context, id, visibility string, progress float64) error {
	_, err := s.db.Exec(ctx, `UPDATE drive_jobs SET provider_visible=$2,provider_progress=$3
WHERE id=$1 AND (provider_visible,provider_progress) IS DISTINCT FROM ($2,$3)`, id, visibility, progress)
	return err
}

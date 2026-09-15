package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ReleaseAssetInput struct {
	Target      string
	Kind        string
	FromVersion string
	FileName    string
	Size        int64
	SHA256      string
	Signature   string
	MirrorsJSON string
}

type ReleaseRecord struct {
	ID            int64  `json:"id"`
	Product       string `json:"product"`
	Channel       string `json:"channel"`
	Version       string `json:"version"`
	Status        string `json:"status"`
	Notes         string `json:"notes"`
	ManifestJSON  string `json:"-"`
	StagedPath    string `json:"-"`
	PublishedPath string `json:"-"`
	CreatedBy     int64  `json:"created_by"`
	CreatedAt     string `json:"created_at"`
	PublishedAt   string `json:"published_at,omitempty"`
}

func (s *Store) StageRelease(
	ctx context.Context,
	record ReleaseRecord,
	assets []ReleaseAssetInput,
) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin stage release: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx,
		`INSERT INTO releases(product,channel,version,status,notes,manifest_json,staged_path,created_by,created_at)
		 VALUES(?1,?2,?3,'staged',?4,?5,?6,?7,?8)`,
		record.Product, record.Channel, record.Version, record.Notes, record.ManifestJSON,
		record.StagedPath, record.CreatedBy, now)
	if err != nil {
		return 0, fmt.Errorf("insert staged release: %w", err)
	}
	releaseID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, asset := range assets {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO release_assets(
				release_id,target,kind,from_version,file_name,size,sha256,signature,mirrors_json
			 ) VALUES(?1,?2,?3,?4,?5,?6,?7,?8,?9)`,
			releaseID, asset.Target, asset.Kind, asset.FromVersion, asset.FileName,
			asset.Size, asset.SHA256, asset.Signature, asset.MirrorsJSON); err != nil {
			return 0, fmt.Errorf("insert release asset: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit staged release: %w", err)
	}
	return releaseID, nil
}

func (s *Store) Release(ctx context.Context, id int64) (ReleaseRecord, error) {
	return scanRelease(s.db.QueryRowContext(ctx,
		`SELECT id,product,channel,version,status,notes,manifest_json,staged_path,published_path,
		        created_by,created_at,published_at
		 FROM releases WHERE id=?1`, id))
}

func (s *Store) ReleaseByIdentity(ctx context.Context, product, channel, version string) (ReleaseRecord, error) {
	return scanRelease(s.db.QueryRowContext(ctx,
		`SELECT id,product,channel,version,status,notes,manifest_json,staged_path,published_path,
		        created_by,created_at,published_at
		 FROM releases WHERE product=?1 AND channel=?2 AND version=?3`, product, channel, version))
}

func (s *Store) ListReleases(ctx context.Context, limit int) ([]ReleaseRecord, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,product,channel,version,status,notes,manifest_json,staged_path,published_path,
		        created_by,created_at,published_at
		 FROM releases ORDER BY id DESC LIMIT ?1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer rows.Close()
	releases := make([]ReleaseRecord, 0)
	for rows.Next() {
		record, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		releases = append(releases, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate releases: %w", err)
	}
	return releases, nil
}

func (s *Store) LegacyPublishedReleases(ctx context.Context) ([]ReleaseRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,product,channel,version,status,notes,manifest_json,staged_path,published_path,
		        created_by,created_at,published_at
		 FROM releases
		 WHERE status IN ('published','superseded') AND published_path LIKE 'releases/%'
		 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list legacy published releases: %w", err)
	}
	defer rows.Close()
	releases := make([]ReleaseRecord, 0)
	for rows.Next() {
		record, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		releases = append(releases, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy published releases: %w", err)
	}
	return releases, nil
}

func (s *Store) UpdatePublishedPath(ctx context.Context, id int64, from, to string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE releases SET published_path=?1
		 WHERE id=?2 AND status IN ('published','superseded') AND published_path=?3`,
		to, id, from)
	if err != nil {
		return fmt.Errorf("update published path: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read published path update result: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRelease(row rowScanner) (ReleaseRecord, error) {
	var record ReleaseRecord
	var createdAt int64
	var publishedAt sql.NullInt64
	err := row.Scan(&record.ID, &record.Product, &record.Channel, &record.Version, &record.Status,
		&record.Notes, &record.ManifestJSON, &record.StagedPath, &record.PublishedPath,
		&record.CreatedBy, &createdAt, &publishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReleaseRecord{}, ErrNotFound
	}
	if err != nil {
		return ReleaseRecord{}, fmt.Errorf("scan release: %w", err)
	}
	record.CreatedAt = time.Unix(createdAt, 0).UTC().Format(time.RFC3339)
	if publishedAt.Valid {
		record.PublishedAt = time.Unix(publishedAt.Int64, 0).UTC().Format(time.RFC3339)
	}
	return record, nil
}

func (s *Store) ChannelHead(ctx context.Context, product, channel string) (ReleaseRecord, error) {
	return scanRelease(s.db.QueryRowContext(ctx,
		`SELECT r.id,r.product,r.channel,r.version,r.status,r.notes,r.manifest_json,r.staged_path,
		        r.published_path,r.created_by,r.created_at,r.published_at
		 FROM channel_heads h JOIN releases r ON r.id=h.release_id
		 WHERE h.product=?1 AND h.channel=?2`, product, channel))
}

func (s *Store) PublishRelease(ctx context.Context, id int64, publishedPath string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin publish release: %w", err)
	}
	defer tx.Rollback()
	var product, channel, status string
	if err := tx.QueryRowContext(ctx,
		`SELECT product,channel,status FROM releases WHERE id=?1`, id).
		Scan(&product, &channel, &status); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read staged release: %w", err)
	}
	if status != "staged" {
		return fmt.Errorf("release is %s, expected staged", status)
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE releases SET status='published',published_path=?1,published_at=?2 WHERE id=?3`,
		publishedPath, now, id); err != nil {
		return fmt.Errorf("mark release published: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE releases SET status='superseded'
		 WHERE product=?1 AND channel=?2 AND id<>?3 AND status='published'`, product, channel, id); err != nil {
		return fmt.Errorf("supersede previous release: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO channel_heads(product,channel,release_id,updated_at) VALUES(?1,?2,?3,?4)
		 ON CONFLICT(product,channel) DO UPDATE SET release_id=excluded.release_id,updated_at=excluded.updated_at`,
		product, channel, id, now); err != nil {
		return fmt.Errorf("update channel head: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit publish release: %w", err)
	}
	return nil
}

func (s *Store) ReleaseAssets(ctx context.Context, releaseID int64) ([]ReleaseAssetInput, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target,kind,from_version,file_name,size,sha256,signature,mirrors_json
		 FROM release_assets WHERE release_id=?1 ORDER BY target,kind,from_version`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list release assets: %w", err)
	}
	defer rows.Close()
	assets := make([]ReleaseAssetInput, 0)
	for rows.Next() {
		var asset ReleaseAssetInput
		if err := rows.Scan(&asset.Target, &asset.Kind, &asset.FromVersion, &asset.FileName,
			&asset.Size, &asset.SHA256, &asset.Signature, &asset.MirrorsJSON); err != nil {
			return nil, fmt.Errorf("scan release asset: %w", err)
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

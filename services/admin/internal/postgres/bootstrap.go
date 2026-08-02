package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type PrincipalSeed struct {
	SPIFFEID    string   `json:"spiffe_id"`
	DisplayName string   `json:"display_name"`
	Enabled     bool     `json:"enabled"`
	Roles       []string `json:"roles"`
}

func (s *Store) Bootstrap(ctx context.Context, seeds []PrincipalSeed) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin admin bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, seed := range seeds {
		if _, err := tx.Exec(ctx, `
INSERT INTO admin_principals (spiffe_id, display_name, enabled)
VALUES ($1,$2,$3)
ON CONFLICT (spiffe_id) DO UPDATE
SET display_name = EXCLUDED.display_name, enabled = EXCLUDED.enabled,
    updated_at = clock_timestamp()`, seed.SPIFFEID, seed.DisplayName, seed.Enabled); err != nil {
			return fmt.Errorf("upsert admin principal: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM admin_role_grants WHERE spiffe_id = $1`, seed.SPIFFEID); err != nil {
			return fmt.Errorf("replace admin role grants: %w", err)
		}
		for _, role := range seed.Roles {
			if _, err := tx.Exec(ctx, `INSERT INTO admin_role_grants (spiffe_id, role) VALUES ($1,$2)`, seed.SPIFFEID, role); err != nil {
				return fmt.Errorf("insert admin role grant: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit admin bootstrap: %w", err)
	}
	return nil
}

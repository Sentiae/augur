package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/sentiae/platform-kit/tenant"
)

// TenantResolverRepo resolves owning orgs via the D-072 SECURITY DEFINER rls_*
// functions (migrations/0001_baseline.up.sql). Each lookup runs under tenant.WithSystemContext so
// the Enforce plugin skips stamping this bootstrap query — the function itself
// resolves the org with definer privileges while the app pool stays NOBYPASSRLS.
type TenantResolverRepo struct {
	db *gorm.DB
}

// NewTenantResolverRepo builds the resolver on the app pool (the rls_* functions
// are EXECUTE-granted to augur_service_app).
func NewTenantResolverRepo(db *gorm.DB) *TenantResolverRepo {
	return &TenantResolverRepo{db: db}
}

// ListOrgIDs enumerates every tenant org for a cross-org background sweep — the
// autoscale/anomaly workers iterate it and re-stamp per org (D-071 Model D). It
// runs the SECURITY DEFINER rls_org_ids() fn under tenant.WithSystemContext so the
// Enforce plugin skips stamping this bootstrap query; the fn resolves the orgs
// with definer privileges while the app pool itself stays NOBYPASSRLS.
func (r *TenantResolverRepo) ListOrgIDs(ctx context.Context) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	sysCtx := tenant.WithSystemContext(ctx)
	if err := r.db.WithContext(sysCtx).Raw(`SELECT * FROM rls_org_ids()`).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("list org ids: %w", err)
	}
	return ids, nil
}

// ResolveWorkloadOrg returns the org owning the workload with the given id, or
// uuid.Nil (no error) when the fn returns NULL (no such row — the miss path). The
// result is scanned into sql.NullString then parsed: scanning a NULL uuid directly
// into *uuid.UUID panics (the fleet gotcha, D-072).
func (r *TenantResolverRepo) ResolveWorkloadOrg(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var raw sql.NullString
	sysCtx := tenant.WithSystemContext(ctx)
	if err := r.db.WithContext(sysCtx).Raw(`SELECT public.rls_workload_org(?)`, id).Scan(&raw).Error; err != nil {
		return uuid.Nil, fmt.Errorf("resolve workload org: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return uuid.Nil, nil
	}
	org, err := uuid.Parse(raw.String)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse resolved workload org: %w", err)
	}
	return org, nil
}

// ResolveDecisionOrg returns the org owning the scaling decision with the given id,
// or uuid.Nil (no error) on a NULL result (the miss path).
func (r *TenantResolverRepo) ResolveDecisionOrg(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var raw sql.NullString
	sysCtx := tenant.WithSystemContext(ctx)
	if err := r.db.WithContext(sysCtx).Raw(`SELECT public.rls_decision_org(?)`, id).Scan(&raw).Error; err != nil {
		return uuid.Nil, fmt.Errorf("resolve decision org: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return uuid.Nil, nil
	}
	org, err := uuid.Parse(raw.String)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse resolved decision org: %w", err)
	}
	return org, nil
}

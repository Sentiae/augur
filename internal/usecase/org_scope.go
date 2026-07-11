package usecase

import (
	"context"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/pkg/config"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
	"github.com/sentiae/platform-kit/tenant"
)

// OrgLister enumerates every tenant org for a cross-org background sweep. It is
// satisfied by the postgres TenantResolverRepo (rls_org_ids() SECURITY DEFINER
// fn); DI passes the concrete impl so this package needn't import postgres.
type OrgLister interface {
	ListOrgIDs(ctx context.Context) ([]uuid.UUID, error)
}

// ForEachOrg runs fn once per tenant org for the D-071 Model-D per-org sweep.
//
// When RLS enforcement is OFF (shadow deploy: app connects as the BYPASSRLS
// superuser), the per-org loop would process every row N times, so fn runs ONCE
// on the original ctx — byte-identical legacy behavior. When enforcement is ON,
// the app pool is NOBYPASSRLS and every read is scoped to app.current_org, so fn
// runs once per org under tenant.WithSystemOrg (which stamps the GUC). A single
// org's failure is logged and skipped — never aborts the whole sweep.
func ForEachOrg(ctx context.Context, lister OrgLister, fn func(orgCtx context.Context) error) error {
	if !config.RLSEnforceEnabled() {
		return fn(ctx)
	}

	orgs, err := lister.ListOrgIDs(ctx)
	if err != nil {
		return err
	}

	for _, org := range orgs {
		if err := fn(tenant.WithSystemOrg(ctx, org)); err != nil {
			logger.Error("ForEachOrg: sweep failed for org %s: %v", org, err)
		}
	}
	return nil
}

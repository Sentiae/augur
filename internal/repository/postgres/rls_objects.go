package postgres

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// P4 Postgres RLS (D-070/071/072) — augur (infrastructure-intelligence-service).
//
// augur builds its schema with GORM AutoMigrate at boot (no golang-migrate). So —
// like notification-service — the RLS objects live here as Go string constants
// applied idempotently under the OWNER connection AFTER AutoMigrate. A clean
// deploy reproduces RLS with zero manual steps.
//
// Each statement is Exec'd one at a time: GORM's db.Exec runs through pgx's
// EXTENDED protocol (a prepared statement), which rejects multiple commands in
// one call (SQLSTATE 42601). splitSQLStatements is dollar-quote aware so a ';'
// inside a function body does not split the statement.
//
// Must be called with the OWNER connection (augur_service_owner): it alters table
// RLS state and transfers function ownership to augur_service_system, which the
// NOBYPASSRLS app role cannot do.
//
// 9 tenant tables get tenant_isolation RLS: augur_workloads, augur_workload_metrics,
// augur_scaling_decisions, augur_policies, augur_slo_definitions,
// augur_slo_burn_rate_log, augur_alerts, augur_cost_budgets, augur_idle_resources.
// augur_model_registry is EXEMPT (global ML metadata, no organization_id) — it is
// deliberately excluded below and never gets RLS.

// zeroOrg is the all-zeros uuid used as the "unset organization" marker; the child
// backfill treats it, like NULL, as a value needing resolution from the parent.
const zeroOrg = "00000000-0000-0000-0000-000000000000"

// rlsObjectsSQL is the full idempotent RLS object script, applied under the owner
// role after AutoMigrate. Order: (a) add organization_id to the two child tables;
// (b) backfill each child's org from its parent, delete orphans (D-046); (c) SET
// NOT NULL + org index on the children; (d) ENABLE+FORCE RLS + tenant_isolation
// policy on the 9 tenant tables; (e) SECURITY DEFINER org-resolver functions owned
// by augur_service_system.
const rlsObjectsSQL = `
-- (a) the two child tables carry no organization_id of their own. Add it (no
-- DEFAULT — it is denormalized from the parent in (b), then made NOT NULL).
ALTER TABLE public.augur_workload_metrics ADD COLUMN IF NOT EXISTS organization_id uuid;
ALTER TABLE public.augur_slo_burn_rate_log ADD COLUMN IF NOT EXISTS organization_id uuid;

-- (b) backfill each child's org from its parent row, then delete rows whose parent
-- is gone (orphans — delete-now-rebuild-later, D-046) so SET NOT NULL succeeds.
UPDATE public.augur_workload_metrics m
   SET organization_id = w.organization_id
  FROM public.augur_workloads w
 WHERE m.workload_id = w.id
   AND (m.organization_id IS NULL OR m.organization_id = '` + zeroOrg + `');
UPDATE public.augur_slo_burn_rate_log l
   SET organization_id = d.organization_id
  FROM public.augur_slo_definitions d
 WHERE l.slo_definition_id = d.id
   AND (l.organization_id IS NULL OR l.organization_id = '` + zeroOrg + `');
DELETE FROM public.augur_workload_metrics  WHERE organization_id IS NULL OR organization_id = '` + zeroOrg + `';
DELETE FROM public.augur_slo_burn_rate_log WHERE organization_id IS NULL OR organization_id = '` + zeroOrg + `';

-- (c) enforce org presence + index it for the RLS predicate on the children.
ALTER TABLE public.augur_workload_metrics  ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE public.augur_slo_burn_rate_log ALTER COLUMN organization_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS idx_augur_metrics_org  ON public.augur_workload_metrics(organization_id);
CREATE INDEX IF NOT EXISTS idx_augur_burnrate_org ON public.augur_slo_burn_rate_log(organization_id);

-- (d) tenant isolation: ENABLE + FORCE RLS with USING (reads/updates/deletes see
-- only the current org) and WITH CHECK (writes belong to the current org). The
-- predicate reads the tx-local app.current_org GUC stamped by
-- platform-kit/tenantdb.Stamp; unset -> NULL -> zero rows (fail-closed).
-- augur_model_registry is EXEMPT (global ML metadata) and gets no policy.
ALTER TABLE public.augur_workloads ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_workloads FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_workloads;
CREATE POLICY tenant_isolation ON public.augur_workloads
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_workload_metrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_workload_metrics FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_workload_metrics;
CREATE POLICY tenant_isolation ON public.augur_workload_metrics
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_scaling_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_scaling_decisions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_scaling_decisions;
CREATE POLICY tenant_isolation ON public.augur_scaling_decisions
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_policies FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_policies;
CREATE POLICY tenant_isolation ON public.augur_policies
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_slo_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_slo_definitions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_slo_definitions;
CREATE POLICY tenant_isolation ON public.augur_slo_definitions
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_slo_burn_rate_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_slo_burn_rate_log FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_slo_burn_rate_log;
CREATE POLICY tenant_isolation ON public.augur_slo_burn_rate_log
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_alerts ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_alerts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_alerts;
CREATE POLICY tenant_isolation ON public.augur_alerts
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_cost_budgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_cost_budgets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_cost_budgets;
CREATE POLICY tenant_isolation ON public.augur_cost_budgets
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_idle_resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_idle_resources FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_idle_resources;
CREATE POLICY tenant_isolation ON public.augur_idle_resources
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

-- (e) SECURITY DEFINER org resolvers owned by augur_service_system (NOLOGIN,
-- BYPASSRLS): the ONLY BYPASSRLS surface, so no BYPASSRLS login credential exists.
-- A pinned search_path prevents search-path hijacking.
--
-- rls_org_ids() enumerates every tenant org for cross-org jobs/consumers (D-071
-- Model D per-org loops) while the app connects NOBYPASSRLS. Union of DISTINCT
-- organization_id over the 7 class-A tables.
CREATE OR REPLACE FUNCTION public.rls_org_ids()
    RETURNS SETOF uuid
    LANGUAGE sql
    SECURITY DEFINER
    SET search_path = public, pg_temp
    STABLE
AS $rls_org_ids$
    SELECT DISTINCT organization_id
    FROM (
        SELECT organization_id FROM public.augur_workloads
        UNION SELECT organization_id FROM public.augur_policies
        UNION SELECT organization_id FROM public.augur_scaling_decisions
        UNION SELECT organization_id FROM public.augur_slo_definitions
        UNION SELECT organization_id FROM public.augur_alerts
        UNION SELECT organization_id FROM public.augur_cost_budgets
        UNION SELECT organization_id FROM public.augur_idle_resources
    ) o
    WHERE organization_id IS NOT NULL
$rls_org_ids$;
ALTER FUNCTION public.rls_org_ids() OWNER TO augur_service_system;
REVOKE EXECUTE ON FUNCTION public.rls_org_ids() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.rls_org_ids() TO augur_service_app;

-- rls_workload_org(p_id) resolves the owning org of a workload by its PK, for by-id
-- reads/mutations that arrive without an org in scope (D-072).
CREATE OR REPLACE FUNCTION public.rls_workload_org(p_id uuid)
    RETURNS uuid
    LANGUAGE sql
    SECURITY DEFINER
    SET search_path = public, pg_temp
    STABLE
AS $rls_workload_org$
    SELECT organization_id FROM public.augur_workloads WHERE id = p_id
$rls_workload_org$;
ALTER FUNCTION public.rls_workload_org(uuid) OWNER TO augur_service_system;
REVOKE EXECUTE ON FUNCTION public.rls_workload_org(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.rls_workload_org(uuid) TO augur_service_app;

-- rls_decision_org(p_id) resolves the owning org of a scaling decision by its PK.
CREATE OR REPLACE FUNCTION public.rls_decision_org(p_id uuid)
    RETURNS uuid
    LANGUAGE sql
    SECURITY DEFINER
    SET search_path = public, pg_temp
    STABLE
AS $rls_decision_org$
    SELECT organization_id FROM public.augur_scaling_decisions WHERE id = p_id
$rls_decision_org$;
ALTER FUNCTION public.rls_decision_org(uuid) OWNER TO augur_service_system;
REVOKE EXECUTE ON FUNCTION public.rls_decision_org(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.rls_decision_org(uuid) TO augur_service_app;

-- the SECURITY DEFINER functions run as augur_service_system, which is BYPASSRLS
-- but has no table grants by default; grant it SELECT on the anchors its resolvers
-- read -- minimal surface (SELECT only).
GRANT SELECT ON public.augur_workloads, public.augur_policies, public.augur_scaling_decisions, public.augur_slo_definitions, public.augur_alerts, public.augur_cost_budgets, public.augur_idle_resources TO augur_service_system;
`

// ApplyRLSObjects (re)applies the RLS objects — child org-column denormalization +
// backfill + NOT NULL, tenant_isolation policies on the 9 tenant tables, and the
// SECURITY DEFINER org resolvers — on top of the AutoMigrate schema. Idempotent;
// MUST be called under the OWNER connection AFTER AutoMigrate.
func ApplyRLSObjects(ownerDB *gorm.DB) error {
	for j, stmt := range splitSQLStatements(rlsObjectsSQL) {
		if err := ownerDB.Exec(stmt).Error; err != nil {
			return fmt.Errorf("apply RLS object stmt #%d: %w", j, err)
		}
	}
	return nil
}

// splitSQLStatements splits a SQL script into individual statements on top-level
// ';', tracking `$tag$ ... $tag$` dollar-quoted blocks (so a ';' inside a
// function body is not a boundary) and skipping '--' line comments. Adequate for
// the RLS DDL scripts (no single-quoted strings containing ';', no /* */ comments);
// it is NOT a general-purpose SQL parser.
func splitSQLStatements(script string) []string {
	var out []string
	var cur strings.Builder
	var dollarTag string // non-empty while inside a $tag$...$tag$ block

	lines := strings.Split(script, "\n")
	for _, line := range lines {
		// Strip a full-line or trailing '--' comment only when not inside a
		// dollar-quoted block.
		if dollarTag == "" {
			if idx := strings.Index(line, "--"); idx >= 0 {
				line = line[:idx]
			}
		}
		cur.WriteString(line)
		cur.WriteString("\n")

		i := 0
		for i < len(line) {
			if dollarTag == "" {
				if line[i] == '$' {
					if tag, n := readDollarTag(line, i); n > 0 {
						dollarTag = tag
						i += n
						continue
					}
				}
				if line[i] == ';' {
					stmt := strings.TrimSpace(cur.String())
					if stmt != "" && stmt != ";" {
						out = append(out, strings.TrimSuffix(stmt, ";")+";")
					}
					cur.Reset()
				}
				i++
			} else {
				if line[i] == '$' {
					if tag, n := readDollarTag(line, i); n > 0 && tag == dollarTag {
						dollarTag = ""
						i += n
						continue
					}
				}
				i++
			}
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// readDollarTag reads a dollar-quote delimiter starting at s[i] (which is '$').
// Returns the full delimiter (e.g. "$$" or "$fn$") and its length, or ("", 0) if
// s[i] does not begin a valid dollar tag.
func readDollarTag(s string, i int) (string, int) {
	if i >= len(s) || s[i] != '$' {
		return "", 0
	}
	j := i + 1
	for j < len(s) && s[j] != '$' {
		c := s[j]
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return "", 0 // not a valid tag body
		}
		j++
	}
	if j >= len(s) {
		return "", 0 // no closing '$'
	}
	return s[i : j+1], j + 1 - i
}

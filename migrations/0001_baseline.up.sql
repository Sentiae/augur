-- 0001_baseline — augur (infrastructure-intelligence-service) schema baseline.
--
-- D-178 golang-migrate standardization. Reproduces, byte-for-byte, the schema
-- that GORM AutoMigrate (the 10 domain models) + ApplyRLSObjects (rls_objects.go)
-- produced at boot, so a fresh deploy builds an identical schema with zero manual
-- steps. augur is greenfield (never deployed) → this is authored from a
-- pg_dump of the real AutoMigrate + RLS output, not hand-transcribed guesses.
--
-- Layout mirrors what the dump showed:
--   (1) 10 tables (9 tenant + augur_model_registry, RLS-exempt) with exact
--       columns/types/defaults/NOT NULL and primary keys;
--   (2) indexes;
--   (3) ENABLE + FORCE ROW LEVEL SECURITY + tenant_isolation policy on the 9
--       tenant tables (augur_model_registry is EXEMPT — global ML metadata);
--   (4) SECURITY DEFINER org-resolver functions owned by augur_service_system,
--       + the SELECT grants its resolvers need.
--
-- The child org-column denormalization/backfill in rls_objects.go (a)-(c) is a
-- no-op on a fresh DB; the resulting schema (organization_id uuid NOT NULL +
-- idx_augur_metrics_org / idx_augur_burnrate_org) is expressed directly below.
--
-- Per-table app-role GRANTs + ALTER DEFAULT PRIVILEGES come from the DB
-- provisioning (infrastructure/docker/init-databases.sql), applied before this
-- migration runs — they are NOT part of this migration.

-- ---------------------------------------------------------------------------
-- (1) Tables
-- ---------------------------------------------------------------------------

CREATE TABLE public.augur_workloads (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    organization_id uuid NOT NULL,
    name text NOT NULL,
    workload_type character varying(20) NOT NULL,
    environment text NOT NULL,
    group_name text,
    feature_id uuid,
    spec_id uuid,
    external_ref text,
    current_replicas bigint DEFAULT 1,
    desired_replicas bigint DEFAULT 1,
    min_replicas bigint DEFAULT 1,
    max_replicas bigint DEFAULT 10,
    optimization_mode character varying(20) DEFAULT 'balanced'::character varying,
    status character varying(20) DEFAULT 'observing'::character varying,
    observe_mode boolean DEFAULT true,
    observe_until timestamp with time zone,
    autoscaling_enabled boolean DEFAULT true,
    autoscaling_paused boolean DEFAULT false,
    paused_until timestamp with time zone,
    pause_reason text,
    monthly_cost_usd numeric DEFAULT 0,
    monthly_budget_usd numeric,
    hourly_cost_usd numeric DEFAULT 0,
    cpu_utilization_pct numeric DEFAULT 0,
    memory_utilization_pct numeric DEFAULT 0,
    requests_per_sec numeric DEFAULT 0,
    latency_p99_ms numeric DEFAULT 0,
    error_rate_pct numeric DEFAULT 0,
    slo_compliance_pct numeric DEFAULT 100,
    consecutive_failures bigint DEFAULT 0,
    circuit_breaker_open boolean DEFAULT false,
    last_scaled_at timestamp with time zone,
    last_metrics_at timestamp with time zone,
    last_decision_at timestamp with time zone,
    last_decision_reasoning text,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    CONSTRAINT augur_workloads_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_workload_metrics (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workload_id uuid NOT NULL,
    organization_id uuid NOT NULL,
    "timestamp" timestamp with time zone NOT NULL,
    cpu_pct numeric,
    memory_pct numeric,
    requests_per_sec numeric,
    latency_p99_ms numeric,
    error_rate_pct numeric,
    replicas bigint,
    cost_usd_per_hour numeric,
    CONSTRAINT augur_workload_metrics_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_scaling_decisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workload_id uuid NOT NULL,
    organization_id uuid NOT NULL,
    trigger character varying(20) NOT NULL,
    direction character varying(10) NOT NULL,
    from_replicas bigint NOT NULL,
    to_replicas bigint NOT NULL,
    reasoning text NOT NULL,
    confidence numeric NOT NULL,
    optimization_mode character varying(20),
    cpu_at_decision numeric,
    memory_at_decision numeric,
    request_rate_at_decision numeric,
    latency_p99_at_decision numeric,
    error_rate_at_decision numeric,
    policy_applied text,
    prediction_used boolean,
    forecast_value numeric,
    est_cost_delta_usd numeric,
    dry_run boolean DEFAULT false,
    requires_approval boolean DEFAULT false,
    approved_by text,
    outcome character varying(20) DEFAULT 'pending'::character varying,
    outcome_observed timestamp with time zone,
    rollback_of_id uuid,
    decided_at timestamp with time zone,
    executed_at timestamp with time zone,
    CONSTRAINT augur_scaling_decisions_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    organization_id uuid NOT NULL,
    scope character varying(10) NOT NULL,
    scope_id text NOT NULL,
    name text NOT NULL,
    description text,
    optimization_mode character varying(20),
    min_replicas bigint,
    max_replicas bigint,
    max_budget_usd numeric,
    enable_spot boolean,
    scaling_rules text,
    enabled boolean DEFAULT true,
    priority bigint DEFAULT 0,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    CONSTRAINT augur_policies_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_slo_definitions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workload_id uuid NOT NULL,
    organization_id uuid NOT NULL,
    slo_type character varying(20) NOT NULL,
    target_pct numeric NOT NULL,
    window_days bigint DEFAULT 30,
    enabled boolean DEFAULT true,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    CONSTRAINT augur_slo_definitions_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_slo_burn_rate_log (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workload_id uuid NOT NULL,
    slo_definition_id uuid NOT NULL,
    organization_id uuid NOT NULL,
    burn_rate1h numeric,
    burn_rate6h numeric,
    burn_rate1d numeric,
    burn_rate3d numeric,
    error_budget_remaining_pct numeric,
    mode character varying(20),
    "timestamp" timestamp with time zone NOT NULL,
    CONSTRAINT augur_slo_burn_rate_log_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_alerts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    organization_id uuid NOT NULL,
    workload_id uuid NOT NULL,
    workload_name text NOT NULL,
    feature_id uuid,
    type character varying(30) NOT NULL,
    severity character varying(20) NOT NULL,
    title text NOT NULL,
    description text,
    auto_action_taken text,
    fired_at timestamp with time zone NOT NULL,
    resolved_at timestamp with time zone,
    created_at timestamp with time zone,
    CONSTRAINT augur_alerts_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_cost_budgets (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    organization_id uuid NOT NULL,
    scope character varying(20) NOT NULL,
    scope_id text NOT NULL,
    budget_usd numeric NOT NULL,
    alert_pcts character varying(100),
    current_spend_usd numeric DEFAULT 0,
    enabled boolean DEFAULT true,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    CONSTRAINT augur_cost_budgets_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_idle_resources (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    organization_id uuid NOT NULL,
    resource_id text NOT NULL,
    resource_type character varying(30) NOT NULL,
    name text NOT NULL,
    environment text NOT NULL,
    idle_since_days bigint DEFAULT 0,
    estimated_monthly_waste_usd numeric DEFAULT 0,
    last_activity_at timestamp with time zone,
    decommissioned boolean DEFAULT false,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    CONSTRAINT augur_idle_resources_pkey PRIMARY KEY (id)
);

-- augur_model_registry — global ML metadata, RLS-EXEMPT (no organization_id).
CREATE TABLE public.augur_model_registry (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    model_name text NOT NULL,
    model_version text NOT NULL,
    model_type character varying(20) NOT NULL,
    file_path text NOT NULL,
    accuracy_mae numeric,
    accuracy_mape numeric,
    trained_at timestamp with time zone,
    active boolean DEFAULT false,
    created_at timestamp with time zone,
    CONSTRAINT augur_model_registry_pkey PRIMARY KEY (id)
);

-- ---------------------------------------------------------------------------
-- (2) Indexes
-- ---------------------------------------------------------------------------

CREATE INDEX idx_augur_workloads_organization_id ON public.augur_workloads USING btree (organization_id);
CREATE INDEX idx_augur_workloads_feature_id ON public.augur_workloads USING btree (feature_id);

CREATE INDEX idx_augur_metrics_org ON public.augur_workload_metrics USING btree (organization_id);
CREATE INDEX idx_metrics_workload_ts ON public.augur_workload_metrics USING btree (workload_id, "timestamp");

CREATE INDEX idx_augur_scaling_decisions_organization_id ON public.augur_scaling_decisions USING btree (organization_id);
CREATE INDEX idx_augur_scaling_decisions_workload_id ON public.augur_scaling_decisions USING btree (workload_id);

CREATE INDEX idx_augur_policies_organization_id ON public.augur_policies USING btree (organization_id);

CREATE INDEX idx_augur_slo_definitions_organization_id ON public.augur_slo_definitions USING btree (organization_id);
CREATE INDEX idx_augur_slo_definitions_workload_id ON public.augur_slo_definitions USING btree (workload_id);

CREATE INDEX idx_augur_burnrate_org ON public.augur_slo_burn_rate_log USING btree (organization_id);
CREATE INDEX idx_burnrate_workload_ts ON public.augur_slo_burn_rate_log USING btree (workload_id, "timestamp");

CREATE INDEX idx_augur_alerts_organization_id ON public.augur_alerts USING btree (organization_id);
CREATE INDEX idx_augur_alerts_workload_id ON public.augur_alerts USING btree (workload_id);

CREATE INDEX idx_augur_cost_budgets_organization_id ON public.augur_cost_budgets USING btree (organization_id);

CREATE INDEX idx_augur_idle_resources_organization_id ON public.augur_idle_resources USING btree (organization_id);

-- ---------------------------------------------------------------------------
-- (3) Row-level security: ENABLE + FORCE + tenant_isolation on the 9 tenant
--     tables. The predicate reads the tx-local app.current_org GUC stamped by
--     platform-kit/tenantdb.Stamp; unset -> NULL -> zero rows (fail-closed).
--     augur_model_registry is EXEMPT and gets no policy.
-- ---------------------------------------------------------------------------

ALTER TABLE public.augur_workloads ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_workloads FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_workloads
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_workload_metrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_workload_metrics FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_workload_metrics
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_scaling_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_scaling_decisions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_scaling_decisions
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_policies
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_slo_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_slo_definitions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_slo_definitions
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_slo_burn_rate_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_slo_burn_rate_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_slo_burn_rate_log
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_alerts ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_alerts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_alerts
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_cost_budgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_cost_budgets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_cost_budgets
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_idle_resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_idle_resources FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_idle_resources
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- (4) SECURITY DEFINER org resolvers, owned by augur_service_system (NOLOGIN,
--     BYPASSRLS) — the only BYPASSRLS surface, so no BYPASSRLS login credential
--     exists. Pinned search_path prevents search-path hijacking.
-- ---------------------------------------------------------------------------

-- rls_org_ids() — enumerates every tenant org for cross-org loops (D-071 Model D)
-- while the app connects NOBYPASSRLS. Union of DISTINCT organization_id over the
-- 7 class-A tables.
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

-- rls_workload_org(p_id) — resolves the owning org of a workload by its PK.
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

-- rls_decision_org(p_id) — resolves the owning org of a scaling decision by its PK.
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

-- The SECURITY DEFINER functions run as augur_service_system (BYPASSRLS) but hold
-- no table grants by default; grant SELECT on the anchors its resolvers read.
GRANT SELECT ON public.augur_workloads, public.augur_policies, public.augur_scaling_decisions, public.augur_slo_definitions, public.augur_alerts, public.augur_cost_budgets, public.augur_idle_resources TO augur_service_system;

-- 0002_agent_identity — augur agent-identity subsystem (D-177, plan §P2b).
--
-- PURELY ADDITIVE on top of 0001_baseline: two org-scoped tenant tables backing
-- enrolled agent identities and their single-use enrollment tokens. Columns,
-- types, defaults, and the tenant_isolation RLS policy mirror 0001_baseline
-- exactly (organization_id uuid NOT NULL; predicate on the app.current_org GUC).
--
-- augur_agents.workload_bindings is JSONB (domain StringSlice → JSON array).

-- ---------------------------------------------------------------------------
-- (1) Tables
-- ---------------------------------------------------------------------------

CREATE TABLE public.augur_agents (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    organization_id uuid NOT NULL,
    agent_type text NOT NULL,
    hostname text,
    status character varying(20) DEFAULT 'pending'::character varying NOT NULL,
    cert_fingerprint text,
    workload_bindings jsonb,
    last_seen_at timestamp with time zone,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    CONSTRAINT augur_agents_pkey PRIMARY KEY (id)
);

CREATE TABLE public.augur_agent_enrollment_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    agent_id uuid,
    organization_id uuid NOT NULL,
    token_hash text,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone,
    CONSTRAINT augur_agent_enrollment_tokens_pkey PRIMARY KEY (id)
);

-- ---------------------------------------------------------------------------
-- (2) Indexes
-- ---------------------------------------------------------------------------

CREATE INDEX idx_augur_agents_organization_id ON public.augur_agents USING btree (organization_id);

CREATE INDEX idx_augur_agent_enrollment_tokens_organization_id ON public.augur_agent_enrollment_tokens USING btree (organization_id);
CREATE INDEX idx_augur_agent_enrollment_tokens_agent_id ON public.augur_agent_enrollment_tokens USING btree (agent_id);
CREATE UNIQUE INDEX idx_augur_agent_enrollment_tokens_token_hash ON public.augur_agent_enrollment_tokens USING btree (token_hash);

-- ---------------------------------------------------------------------------
-- (3) Row-level security: ENABLE + FORCE + tenant_isolation on both tables,
--     identical to 0001_baseline (fail-closed on an unset app.current_org GUC).
-- ---------------------------------------------------------------------------

ALTER TABLE public.augur_agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_agents FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_agents;
CREATE POLICY tenant_isolation ON public.augur_agents
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_agent_enrollment_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_agent_enrollment_tokens FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_agent_enrollment_tokens;
CREATE POLICY tenant_isolation ON public.augur_agent_enrollment_tokens
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

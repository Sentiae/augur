-- 0003_agent_tables_rls_exempt — augur (D-177 P8 fix).
--
-- augur_agents + augur_agent_enrollment_tokens are CONTROL-PLANE tables, not
-- tenant business data: no tenant-scoped app query is ever issued against them.
-- Every access path is system-context —
--   * enrollment (find token by hash [pre-auth, cross-org], find agent, consume
--     token, activate agent) runs under tenant.WithSystemContext, and
--   * the agent-plane mTLS interceptor cross-checks the agent row by the id parsed
--     from the client cert (also system-context).
-- Org isolation for agents is therefore enforced at the APPLICATION layer (the
-- interceptor requires cert-org == agent-row org and workload ∈ the agent's
-- bindings), exactly like the outbox and augur_model_registry are RLS-exempt.
--
-- 0002 gave these two tables tenant_isolation FORCE RLS. That is actively WRONG:
-- augur runs its app statements as the NOBYPASSRLS augur_service_app role, and
-- tenantdb.Enforce SKIPS stamping the org GUC under WithSystemContext (it assumes
-- a BYPASSRLS path). With FORCE RLS + unset GUC the policy matches zero rows, so
-- the pre-auth token lookup fails closed and NO agent can ever enroll. Proven live
-- on the homelab (P8): enroll returned "invalid enrollment token" until RLS was
-- lifted, after which enroll issued a cert with the correct agent-row org URI SAN.
-- (§24: never edit 0002 — this migration corrects it.)

ALTER TABLE public.augur_agents NO FORCE ROW LEVEL SECURITY;
ALTER TABLE public.augur_agents DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_agents;

ALTER TABLE public.augur_agent_enrollment_tokens NO FORCE ROW LEVEL SECURITY;
ALTER TABLE public.augur_agent_enrollment_tokens DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_agent_enrollment_tokens;

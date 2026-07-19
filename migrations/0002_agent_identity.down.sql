-- 0002_agent_identity (down) — drop everything 0002_agent_identity.up.sql
-- created, in reverse order: the policies (dropped implicitly with the tables,
-- but stated explicitly to mirror the up file), then the tables (their RLS
-- state, indexes, and PK constraints drop with them).

DROP POLICY IF EXISTS tenant_isolation ON public.augur_agent_enrollment_tokens;
DROP POLICY IF EXISTS tenant_isolation ON public.augur_agents;

DROP TABLE IF EXISTS public.augur_agent_enrollment_tokens;
DROP TABLE IF EXISTS public.augur_agents;

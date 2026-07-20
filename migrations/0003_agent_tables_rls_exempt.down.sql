-- Reverse 0003: restore the tenant_isolation FORCE RLS that 0002 put on the two
-- agent tables. (Restores 0002's state exactly; note that with RLS re-enabled the
-- enrollment path fails closed under the NOBYPASSRLS app role — see 0003 up.)

ALTER TABLE public.augur_agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_agents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_agents
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

ALTER TABLE public.augur_agent_enrollment_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.augur_agent_enrollment_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.augur_agent_enrollment_tokens
    USING (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.current_org', true), '')::uuid);

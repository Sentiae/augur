-- 0001_baseline (down) — drop everything 0001_baseline.up.sql created, in reverse
-- dependency order: the SECURITY DEFINER resolver functions first (SQL functions
-- carry hard dependencies on the tables they read), then the tables (their
-- policies, RLS state, indexes, and PK constraints drop with them).

DROP FUNCTION IF EXISTS public.rls_decision_org(uuid);
DROP FUNCTION IF EXISTS public.rls_workload_org(uuid);
DROP FUNCTION IF EXISTS public.rls_org_ids();

DROP TABLE IF EXISTS public.augur_model_registry;
DROP TABLE IF EXISTS public.augur_idle_resources;
DROP TABLE IF EXISTS public.augur_cost_budgets;
DROP TABLE IF EXISTS public.augur_alerts;
DROP TABLE IF EXISTS public.augur_slo_burn_rate_log;
DROP TABLE IF EXISTS public.augur_slo_definitions;
DROP TABLE IF EXISTS public.augur_policies;
DROP TABLE IF EXISTS public.augur_scaling_decisions;
DROP TABLE IF EXISTS public.augur_workload_metrics;
DROP TABLE IF EXISTS public.augur_workloads;

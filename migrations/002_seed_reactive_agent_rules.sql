-- Augur Reactive Agent Rules
-- These rules are consumed by foundry-service's reactive agent system.
-- They bridge Augur's fast autonomous loop with LLM-powered reasoning.
-- Table: foundry-service manages reactive_agent_rules; these INSERTs are
-- provided as a seed script to run against the foundry DB when ready.

-- Rule 1: SLO Breach → Diagnose and Remediate
INSERT INTO reactive_agent_rules
  (name, trigger_event, event_filter, agent_prompt, auto_execute, enabled)
VALUES
  ('Augur: SLO Breach — Diagnose and remediate',
   'sentiae.augur.slo.breach_detected',
   '{"burn_rate": {"$gte": 6}}',
   'SLO breach on workload {{event.data.workload_id}} (Feature: {{event.data.feature_id}}).

Burn rate: {{event.data.burn_rate}}x — error budget will be exhausted in {{event.data.budget_pct_left}}% remaining.
SLO type: {{event.data.slo_type}} | Target: {{event.data.slo_target}}% | Current: {{event.data.current_value}}%.
Deploy-correlated: {{event.data.deploy_correlated}}.
Augur action taken: {{event.data.action_taken}}.

1. Run diagnose_workload to understand root cause.
2. Check get_slo_status for full burn rate picture.
3. If deploy_correlated is true and a deploy happened within the last 30 minutes, evaluate rollback (requires confirmation).
4. If infrastructure-related, check if scaling bounds need adjustment.
5. Create a Spec inside the Feature if this is not already a known issue.
6. Report findings with recommended next steps.',
   true, true)
ON CONFLICT DO NOTHING;

-- Rule 2: Anomaly Detected → Investigate
INSERT INTO reactive_agent_rules
  (name, trigger_event, event_filter, agent_prompt, auto_execute, enabled)
VALUES
  ('Augur: Anomaly detected — Investigate',
   'sentiae.augur.anomaly.detected',
   '{"severity": "critical", "confidence": {"$gte": 0.85}}',
   'Critical anomaly on workload {{event.data.workload_id}}.

Type: {{event.data.anomaly_type}} | Confidence: {{event.data.confidence}}
Affected metrics: {{event.data.affected_metrics}}
Description: {{event.data.description}}
Deploy-correlated: {{event.data.deploy_correlated}}
Suggested action: {{event.data.suggested_action}}

1. Get current workload metrics and SLO status.
2. If deploy-correlated: check recent deployments, evaluate rollback.
3. If not deploy-correlated: check if scaling bounds need adjustment.
4. Create a Spec in the associated Feature if this is a recurring issue.
5. Report root cause hypothesis and recommended action.',
   true, true)
ON CONFLICT DO NOTHING;

-- Rule 3: Spike Predicted → Validate Pre-Scale
INSERT INTO reactive_agent_rules
  (name, trigger_event, event_filter, agent_prompt, auto_execute, enabled)
VALUES
  ('Augur: Spike predicted — Validate pre-scale plan',
   'sentiae.augur.forecast.spike_predicted',
   '{"predicted_peak_pct": {"$gte": 100}, "confidence": {"$gte": 0.80}}',
   'Traffic spike predicted for workload {{event.data.workload_id}}.

Expected peak: +{{event.data.predicted_peak_pct}}% above baseline at {{event.data.predicted_peak_at}}.
Horizon: {{event.data.horizon_minutes}} minutes | Confidence: {{event.data.confidence}} | Model: {{event.data.model_used}}.
Pre-scale already initiated by Augur: {{event.data.pre_scale_initiated}}.

1. Confirm current SLO status and error budget.
2. If pre-scale was NOT initiated and horizon < 30 minutes, scale up now.
3. Check active deployments — a deploy during a traffic spike is high-risk.
4. If spike > 300% baseline, notify the Feature team.',
   true, true)
ON CONFLICT DO NOTHING;

-- Rule 4: Cost Threshold → Investigate and Optimize
INSERT INTO reactive_agent_rules
  (name, trigger_event, event_filter, agent_prompt, auto_execute, enabled)
VALUES
  ('Augur: Cost threshold exceeded — Investigate',
   'sentiae.augur.cost.threshold_exceeded',
   '{"threshold_pct": {"$gte": 90}}',
   'Cost threshold crossed for {{event.data.scope}} ''{{event.data.scope_id}}''.

Budget: ${{event.data.budget_usd}} | Actual: ${{event.data.actual_usd}} | Projected: ${{event.data.projected_usd}}
Threshold: {{event.data.threshold_pct}}%.

1. Get the full cost report for this scope.
2. Identify workloads with spot migration or rightsizing opportunities.
3. If any workloads in performance mode have idle periods, recommend switching to balanced.
4. List idle resources eligible for decommission.
5. If projected overage > 20%, create a Spec: ''Reduce infrastructure costs for [scope]''.',
   true, true)
ON CONFLICT DO NOTHING;

-- Rule 5: Spot Interruption Predicted → Graceful Migration
INSERT INTO reactive_agent_rules
  (name, trigger_event, event_filter, agent_prompt, auto_execute, enabled)
VALUES
  ('Augur: Spot interruption predicted — Migrate gracefully',
   'sentiae.augur.spot.interruption_predicted',
   '{}',
   'Spot interruption predicted for workload {{event.data.workload_id}} in {{event.data.horizon_minutes}} minutes.

1. Check SLO status — if error budget is low, pre-provision on-demand capacity immediately.
2. Verify replica count is above the minimum safety floor.
3. If replica count is at minimum, scale up one on-demand replica now.
4. Report migration plan.',
   true, true)
ON CONFLICT DO NOTHING;

-- Rule 6: Circuit Breaker Opened → Escalate
INSERT INTO reactive_agent_rules
  (name, trigger_event, event_filter, agent_prompt, auto_execute, enabled)
VALUES
  ('Augur: Circuit breaker opened — Escalate',
   'sentiae.augur.circuit_breaker.opened',
   '{}',
   'Augur circuit breaker opened for workload {{event.data.workload_id}} after repeated scaling failures. Auto-scaling is now suspended.

1. Run diagnose_workload to understand why scaling is failing.
2. Check recent deployments — a bad deploy may be causing crash loops.
3. If rollback is needed, request confirmation before executing.
4. Create a high-priority Spec in the associated Feature.
5. Report findings — the team needs to know auto-scaling is suspended.',
   true, true)
ON CONFLICT DO NOTHING;

-- Rule 7: Deploy Completed → Enter Observation Mode
INSERT INTO reactive_agent_rules
  (name, trigger_event, event_filter, agent_prompt, auto_execute, enabled)
VALUES
  ('Augur: Deploy completed — Enter observation mode',
   'sentiae.ops.deploy.completed',
   '{}',
   'Deploy completed for environment {{event.data.environment_id}}.

1. Pause auto-scale-down for affected workloads for 15 minutes.
2. Check SLO status 5 minutes post-deploy.
3. If SLO burn rate exceeds 3x baseline within 15 minutes, flag as deploy-correlated and alert.
4. After 15 minutes of healthy metrics, resume normal Augur operation.',
   true, true)
ON CONFLICT DO NOTHING;

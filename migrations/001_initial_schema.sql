-- Augur Service: Initial schema
-- All tables use augur_ prefix to coexist in the shared platform PostgreSQL instance

-- Enable UUID generation
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Workload Registry
CREATE TABLE IF NOT EXISTS augur_workloads (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id       UUID NOT NULL,
    name                  VARCHAR(255) NOT NULL,
    workload_type         VARCHAR(20) NOT NULL,  -- firecracker, kubernetes, vm, serverless
    environment           VARCHAR(100) NOT NULL,
    group_name            VARCHAR(255),
    feature_id            UUID,
    spec_id               UUID,
    external_ref          VARCHAR(500),
    current_replicas      INT DEFAULT 1,
    desired_replicas      INT DEFAULT 1,
    min_replicas          INT DEFAULT 1,
    max_replicas          INT DEFAULT 10,
    optimization_mode     VARCHAR(20) DEFAULT 'balanced',
    status                VARCHAR(20) DEFAULT 'observing',
    observe_mode          BOOLEAN DEFAULT TRUE,
    observe_until         TIMESTAMPTZ,
    autoscaling_enabled   BOOLEAN DEFAULT TRUE,
    autoscaling_paused    BOOLEAN DEFAULT FALSE,
    paused_until          TIMESTAMPTZ,
    pause_reason          TEXT,
    monthly_cost_usd      DOUBLE PRECISION DEFAULT 0,
    monthly_budget_usd    DOUBLE PRECISION,
    hourly_cost_usd       DOUBLE PRECISION DEFAULT 0,
    cpu_utilization_pct   DOUBLE PRECISION DEFAULT 0,
    memory_utilization_pct DOUBLE PRECISION DEFAULT 0,
    requests_per_sec      DOUBLE PRECISION DEFAULT 0,
    latency_p99_ms        DOUBLE PRECISION DEFAULT 0,
    error_rate_pct        DOUBLE PRECISION DEFAULT 0,
    slo_compliance_pct    DOUBLE PRECISION DEFAULT 100,
    consecutive_failures  INT DEFAULT 0,
    circuit_breaker_open  BOOLEAN DEFAULT FALSE,
    last_scaled_at        TIMESTAMPTZ,
    last_metrics_at       TIMESTAMPTZ,
    last_decision_at      TIMESTAMPTZ,
    last_decision_reasoning TEXT,
    created_at            TIMESTAMPTZ DEFAULT NOW(),
    updated_at            TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_workloads_org ON augur_workloads(organization_id);
CREATE INDEX idx_workloads_feature ON augur_workloads(feature_id) WHERE feature_id IS NOT NULL;

-- Workload Metrics Snapshots (time series stored in PostgreSQL for Phase 1, VictoriaMetrics later)
CREATE TABLE IF NOT EXISTS augur_workload_metrics (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workload_id       UUID NOT NULL REFERENCES augur_workloads(id) ON DELETE CASCADE,
    timestamp         TIMESTAMPTZ NOT NULL,
    cpu_pct           DOUBLE PRECISION,
    memory_pct        DOUBLE PRECISION,
    requests_per_sec  DOUBLE PRECISION,
    latency_p99_ms    DOUBLE PRECISION,
    error_rate_pct    DOUBLE PRECISION,
    replicas          INT,
    cost_usd_per_hour DOUBLE PRECISION
);

CREATE INDEX idx_metrics_workload_ts ON augur_workload_metrics(workload_id, timestamp DESC);

-- Scaling Decisions (full audit log)
CREATE TABLE IF NOT EXISTS augur_scaling_decisions (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workload_id            UUID NOT NULL REFERENCES augur_workloads(id) ON DELETE CASCADE,
    organization_id        UUID NOT NULL,
    trigger                VARCHAR(20) NOT NULL,  -- reactive, predictive, slo, manual, cost
    direction              VARCHAR(10) NOT NULL,   -- up, down, none
    from_replicas          INT NOT NULL,
    to_replicas            INT NOT NULL,
    reasoning              TEXT NOT NULL,
    confidence             DOUBLE PRECISION NOT NULL,
    optimization_mode      VARCHAR(20),
    cpu_at_decision        DOUBLE PRECISION,
    memory_at_decision     DOUBLE PRECISION,
    request_rate_at_decision DOUBLE PRECISION,
    latency_p99_at_decision  DOUBLE PRECISION,
    error_rate_at_decision   DOUBLE PRECISION,
    policy_applied         TEXT,
    prediction_used        BOOLEAN DEFAULT FALSE,
    forecast_value         DOUBLE PRECISION,
    est_cost_delta_usd     DOUBLE PRECISION,
    dry_run                BOOLEAN DEFAULT FALSE,
    requires_approval      BOOLEAN DEFAULT FALSE,
    approved_by            VARCHAR(255),
    outcome                VARCHAR(20) DEFAULT 'pending',  -- pending, healthy, degraded, rolled_back, failed
    outcome_observed       TIMESTAMPTZ,
    rollback_of_id         UUID,
    decided_at             TIMESTAMPTZ DEFAULT NOW(),
    executed_at            TIMESTAMPTZ
);

CREATE INDEX idx_decisions_workload ON augur_scaling_decisions(workload_id);
CREATE INDEX idx_decisions_org ON augur_scaling_decisions(organization_id);
CREATE INDEX idx_decisions_decided ON augur_scaling_decisions(decided_at DESC);

-- Policy Definitions (three-tier hierarchy)
CREATE TABLE IF NOT EXISTS augur_policies (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL,
    scope             VARCHAR(10) NOT NULL,   -- global, group, app
    scope_id          VARCHAR(255) NOT NULL,  -- org ID, group name, workload ID
    name              VARCHAR(255) NOT NULL,
    description       TEXT,
    optimization_mode VARCHAR(20),
    min_replicas      INT,
    max_replicas      INT,
    max_budget_usd    DOUBLE PRECISION,
    enable_spot       BOOLEAN,
    scaling_rules     TEXT,  -- JSON-encoded CEL rules
    enabled           BOOLEAN DEFAULT TRUE,
    priority          INT DEFAULT 0,
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    updated_at        TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_policies_org ON augur_policies(organization_id);
CREATE INDEX idx_policies_scope ON augur_policies(scope, scope_id);

-- SLO Definitions
CREATE TABLE IF NOT EXISTS augur_slo_definitions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workload_id     UUID NOT NULL REFERENCES augur_workloads(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL,
    slo_type        VARCHAR(20) NOT NULL,  -- availability, latency, error_rate
    target_pct      DOUBLE PRECISION NOT NULL,
    window_days     INT DEFAULT 30,
    enabled         BOOLEAN DEFAULT TRUE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_slo_workload ON augur_slo_definitions(workload_id);

-- SLO Burn Rate Log
CREATE TABLE IF NOT EXISTS augur_slo_burn_rate_log (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workload_id              UUID NOT NULL,
    slo_definition_id        UUID NOT NULL REFERENCES augur_slo_definitions(id) ON DELETE CASCADE,
    burn_rate_1h             DOUBLE PRECISION,
    burn_rate_6h             DOUBLE PRECISION,
    burn_rate_1d             DOUBLE PRECISION,
    burn_rate_3d             DOUBLE PRECISION,
    error_budget_remaining_pct DOUBLE PRECISION,
    mode                     VARCHAR(20),  -- normal, warning, critical, emergency
    timestamp                TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_burnrate_workload_ts ON augur_slo_burn_rate_log(workload_id, timestamp DESC);

-- Alerts
CREATE TABLE IF NOT EXISTS augur_alerts (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL,
    workload_id       UUID NOT NULL,
    workload_name     VARCHAR(255) NOT NULL,
    feature_id        UUID,
    type              VARCHAR(30) NOT NULL,   -- slo_breach, anomaly, cost_threshold, spot_interruption, circuit_breaker
    severity          VARCHAR(20) NOT NULL,   -- warning, critical
    title             VARCHAR(500) NOT NULL,
    description       TEXT,
    auto_action_taken TEXT,
    fired_at          TIMESTAMPTZ NOT NULL,
    resolved_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_alerts_org ON augur_alerts(organization_id);
CREATE INDEX idx_alerts_workload ON augur_alerts(workload_id);
CREATE INDEX idx_alerts_active ON augur_alerts(organization_id) WHERE resolved_at IS NULL;

-- Cost Budgets
CREATE TABLE IF NOT EXISTS augur_cost_budgets (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL,
    scope             VARCHAR(20) NOT NULL,   -- organization, group, workload
    scope_id          VARCHAR(255) NOT NULL,
    budget_usd        DOUBLE PRECISION NOT NULL,
    alert_pcts        VARCHAR(100),  -- comma-separated: "80,90,100"
    current_spend_usd DOUBLE PRECISION DEFAULT 0,
    enabled           BOOLEAN DEFAULT TRUE,
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    updated_at        TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_budgets_org ON augur_cost_budgets(organization_id);

-- Idle Resources
CREATE TABLE IF NOT EXISTS augur_idle_resources (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id           UUID NOT NULL,
    resource_id               VARCHAR(500) NOT NULL,
    resource_type             VARCHAR(30) NOT NULL,  -- compute, storage, namespace
    name                      VARCHAR(255) NOT NULL,
    environment               VARCHAR(100) NOT NULL,
    idle_since_days           INT DEFAULT 0,
    estimated_monthly_waste_usd DOUBLE PRECISION DEFAULT 0,
    last_activity_at          TIMESTAMPTZ,
    decommissioned            BOOLEAN DEFAULT FALSE,
    created_at                TIMESTAMPTZ DEFAULT NOW(),
    updated_at                TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_idle_org ON augur_idle_resources(organization_id);

-- Model Registry (for ONNX models in Phase 2+)
CREATE TABLE IF NOT EXISTS augur_model_registry (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_name     VARCHAR(255) NOT NULL,
    model_version  VARCHAR(50) NOT NULL,
    model_type     VARCHAR(20) NOT NULL,  -- holt_winters, n_hits, tft, chronos
    file_path      VARCHAR(500) NOT NULL,
    accuracy_mae   DOUBLE PRECISION,
    accuracy_mape  DOUBLE PRECISION,
    trained_at     TIMESTAMPTZ,
    active         BOOLEAN DEFAULT FALSE,
    created_at     TIMESTAMPTZ DEFAULT NOW()
);

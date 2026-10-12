-- Pond platform database schema

CREATE TABLE clusters
(
    id               UUID PRIMARY KEY,
    name             TEXT        NOT NULL UNIQUE,
    agent_token_hash TEXT        NOT NULL,
    last_seen_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE projects
(
    id                  UUID PRIMARY KEY,
    name                TEXT        NOT NULL UNIQUE,
    root_environment_id UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE environments
(
    id                        UUID PRIMARY KEY,
    project_id                UUID        NOT NULL REFERENCES projects (id),
    parent_environment_id     UUID REFERENCES environments (id),
    name                      TEXT        NOT NULL,
    namespace                 TEXT        NOT NULL,
    default_ingress_base_host TEXT        NOT NULL DEFAULT '',
    cluster_id                UUID        NOT NULL REFERENCES clusters (id),
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, name)
);

-- current_deployment_id is the most recently submitted deployment, whose
-- dependency input the next one reuses; running_deployment_id is the most
-- recently succeeded one.
CREATE TABLE services
(
    id                    UUID PRIMARY KEY,
    project_id            UUID        NOT NULL REFERENCES projects (id),
    name                  TEXT        NOT NULL,
    current_deployment_id UUID,
    running_deployment_id UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, name)
);

-- status is in_progress until the deployment succeeds, fails or is cancelled.
-- Finer progress is computed from its commands.
CREATE TABLE deployments
(
    id                      UUID PRIMARY KEY,
    service_id              UUID        NOT NULL REFERENCES services (id),
    environment_id          UUID        NOT NULL REFERENCES environments (id),
    image_tag               TEXT        NOT NULL,
    service_config_snapshot JSONB       NOT NULL,
    status                  TEXT        NOT NULL DEFAULT 'in_progress',
    triggered_by            TEXT,
    helm_command_id         UUID,
    error                   TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at            TIMESTAMPTZ
);

-- A dependency as one deployment resolved it. Rows do not change after
-- submit: a managed dependency's progress and outputs are its command's, and
-- an unmanaged one's outputs are its user_config.
CREATE TABLE dependency_deployments
(
    id              UUID PRIMARY KEY,
    deployment_id   UUID    NOT NULL REFERENCES deployments (id),
    dependency_name TEXT    NOT NULL,
    dependency_type TEXT    NOT NULL,
    managed         BOOLEAN NOT NULL,
    provider_inputs JSONB,
    user_config     JSONB,
    command_id      UUID,
    UNIQUE (deployment_id, dependency_name)
);

-- A deployment's commands run in phases: dependencies (0), then the rollout
-- (1). A command waits until every command of its deployment in a lower phase
-- has succeeded. A payload built from an earlier phase's outputs is written
-- when its own phase starts, so it is empty until then.
CREATE TABLE commands
(
    id            UUID PRIMARY KEY,
    cluster_id    UUID        NOT NULL REFERENCES clusters (id),
    deployment_id UUID        NOT NULL REFERENCES deployments (id),
    type          TEXT        NOT NULL,
    phase         INT         NOT NULL DEFAULT 0,
    payload       JSONB,
    status        TEXT        NOT NULL DEFAULT 'queued',
    output        JSONB,
    error         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX commands_deployment ON commands (deployment_id);
-- Serves the agents' claim of the next queued command of their cluster.
CREATE INDEX commands_cluster_status ON commands (cluster_id, status, created_at);

CREATE TABLE command_logs
(
    command_id UUID        NOT NULL REFERENCES commands (id),
    line       TEXT        NOT NULL,
    logged_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX command_logs_command ON command_logs (command_id, logged_at);

-- Pond platform database schema
-- Derived from internal/server/store/ SQL queries

CREATE TABLE organizations
(
    id         UUID PRIMARY KEY,
    name       TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE clusters
(
    id               UUID PRIMARY KEY,
    organization_id  UUID        NOT NULL REFERENCES organizations (id),
    name             TEXT        NOT NULL,
    agent_token_hash TEXT        NOT NULL,
    last_seen_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE projects
(
    id                  UUID PRIMARY KEY,
    organization_id     UUID        NOT NULL REFERENCES organizations (id),
    name                TEXT        NOT NULL,
    root_environment_id UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, name)
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

CREATE TABLE deployments
(
    id                      UUID PRIMARY KEY,
    service_id              UUID        NOT NULL REFERENCES services (id),
    environment_id          UUID        NOT NULL REFERENCES environments (id),
    image_tag               TEXT        NOT NULL,
    service_config_snapshot JSONB       NOT NULL,
    status                  TEXT        NOT NULL DEFAULT 'pending',
    triggered_by            TEXT,
    helm_command_id         UUID,
    error                   TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at            TIMESTAMPTZ
);


CREATE TABLE dependency_deployments
(
    id              UUID PRIMARY KEY,
    deployment_id   UUID        NOT NULL REFERENCES deployments (id),
    dependency_name TEXT        NOT NULL,
    dependency_type TEXT        NOT NULL,
    managed         BOOLEAN, -- NULL while awaiting_input
    provider_inputs JSONB,
    user_config     JSONB,
    status          TEXT        NOT NULL,
    command_id      UUID,
    output          JSONB,
    -- status: awaiting_input | pending | succeeded | failed
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


CREATE TABLE commands
(
    id            UUID PRIMARY KEY,
    cluster_id    UUID        NOT NULL REFERENCES clusters (id),
    deployment_id UUID        NOT NULL REFERENCES deployments (id),
    type          TEXT        NOT NULL,
    payload       JSONB       NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'queued',
    output        JSONB,
    error         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


CREATE TABLE command_logs
(
    command_id UUID        NOT NULL REFERENCES commands (id),
    line       TEXT        NOT NULL,
    logged_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX command_logs_command on command_logs (command_id, logged_at);

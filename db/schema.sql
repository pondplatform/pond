-- Pond platform database schema
-- Derived from internal/server/store/ SQL queries

CREATE TABLE organizations (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE clusters (
    id UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    agent_token_hash TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE projects (
    id UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    root_environment_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(organization_id, name)
);

CREATE TABLE environments (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id),
    parent_environment_id UUID REFERENCES environments(id),
    name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    default_ingress_base_host TEXT NOT NULL DEFAULT '',
    cluster_id UUID NOT NULL REFERENCES clusters(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, name)
);

CREATE TABLE services (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, name)
);

CREATE TABLE deployments (
    id UUID PRIMARY KEY,
    service_id UUID NOT NULL REFERENCES services(id),
    environment_id UUID NOT NULL REFERENCES environments(id),
    image_tag TEXT NOT NULL,
    service_config_snapshot JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    triggered_by TEXT NOT NULL,
    helm_command_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE command_queue (
    id UUID PRIMARY KEY,
    cluster_id UUID NOT NULL REFERENCES clusters(id),
    deployment_id UUID NOT NULL REFERENCES deployments(id),
    type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE command_results (
    command_id UUID PRIMARY KEY,
    success BOOLEAN NOT NULL,
    output JSONB,
    error TEXT,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE dependency_deployment_requests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id    UUID NOT NULL REFERENCES deployments(id),
    command_id       UUID NOT NULL,
    dependency_name  TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending',
    output           JSONB,
    completed_at     TIMESTAMPTZ
);

CREATE TABLE dependency_configs (
    id UUID PRIMARY KEY,
    service_id UUID NOT NULL,
    environment_id UUID NOT NULL,
    dependency_name TEXT NOT NULL,
    dependency_type TEXT NOT NULL,
    managed BOOLEAN NOT NULL DEFAULT false,
    provider_inputs JSONB,
    user_config JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(service_id, environment_id, dependency_name)
);

CREATE TABLE resolved_contexts (
    id UUID PRIMARY KEY,
    service_id UUID NOT NULL,
    environment_id UUID NOT NULL,
    dependency_name TEXT NOT NULL,
    values JSONB,
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source_deployment_id UUID,
    UNIQUE(service_id, environment_id, dependency_name)
);

ALTER TABLE dependency_deployments ADD COLUMN completed_at TIMESTAMPTZ;

-- Every dependency row is addressed by this pair; the constraint's index also
-- serves lookups by deployment.
ALTER TABLE dependency_deployments
    ADD CONSTRAINT dependency_deployments_deployment_name_key UNIQUE (deployment_id, dependency_name);

CREATE INDEX commands_deployment ON commands (deployment_id);
CREATE INDEX commands_cluster_status ON commands (cluster_id, status, created_at);

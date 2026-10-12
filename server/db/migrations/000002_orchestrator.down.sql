DROP INDEX IF EXISTS commands_cluster_status;
DROP INDEX IF EXISTS commands_deployment;

ALTER TABLE dependency_deployments DROP CONSTRAINT IF EXISTS dependency_deployments_deployment_name_key;
ALTER TABLE dependency_deployments DROP COLUMN IF EXISTS completed_at;

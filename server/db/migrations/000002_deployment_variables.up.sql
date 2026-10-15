-- Kept with the deployment because the rollout that renders them runs after the submit.
ALTER TABLE deployments ADD COLUMN variables JSONB NOT NULL DEFAULT '{}';

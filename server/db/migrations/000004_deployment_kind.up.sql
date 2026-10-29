-- A delete deployment tears its service down in its environment through one
-- service.delete command. A service whose latest deployment in every
-- environment is a succeeded delete is deleted.
ALTER TABLE deployments ADD COLUMN kind TEXT NOT NULL DEFAULT 'deploy';
-- Serves the latest deployment of a service per environment.
CREATE INDEX deployments_service_environment ON deployments (service_id, environment_id, created_at DESC);

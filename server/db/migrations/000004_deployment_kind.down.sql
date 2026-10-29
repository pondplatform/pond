DROP INDEX IF EXISTS deployments_service_environment;
ALTER TABLE deployments DROP COLUMN IF EXISTS kind;

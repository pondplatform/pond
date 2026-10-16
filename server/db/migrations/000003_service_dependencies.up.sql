ALTER TABLE environments ADD COLUMN gateway_port INT;
ALTER TABLE environments ADD COLUMN gateway_tls BOOLEAN NOT NULL DEFAULT true;
-- Rendered at rollout so services depending on this one never re-render its templates.
ALTER TABLE deployments ADD COLUMN exports JSONB;

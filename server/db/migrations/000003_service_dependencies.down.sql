ALTER TABLE deployments DROP COLUMN IF EXISTS exports;
ALTER TABLE environments DROP COLUMN IF EXISTS gateway_tls;
ALTER TABLE environments DROP COLUMN IF EXISTS gateway_port;

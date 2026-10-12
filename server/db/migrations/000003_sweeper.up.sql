-- The sweeper's scans: commands awaiting a result, and unfinished deployments.
CREATE INDEX commands_status_updated ON commands (status, updated_at);
CREATE INDEX deployments_unfinished ON deployments (created_at)
    WHERE status IN ('pending', 'running', 'awaiting_input');

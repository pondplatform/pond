-- At most one deployment of a service is in progress per environment. Of any
-- that already are, all but the newest are cancelled first, the way a cancel
-- would: their queued commands are cancelled, and the agent is told to stop
-- the one it runs.
WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY service_id, environment_id ORDER BY created_at DESC, id DESC) AS n
      FROM deployments
     WHERE status = 'in_progress'
),
superseded AS (
    UPDATE deployments
       SET status = 'cancelled', completed_at = NOW(),
           error = 'cancelled on upgrade: a newer deployment of the service to this environment was in progress'
     WHERE id IN (SELECT id FROM ranked WHERE n > 1)
    RETURNING id
)
UPDATE commands SET status = 'cancelled', updated_at = NOW()
 WHERE status = 'queued' AND deployment_id IN (SELECT id FROM superseded);

CREATE UNIQUE INDEX deployments_one_in_progress ON deployments (service_id, environment_id) WHERE status = 'in_progress';

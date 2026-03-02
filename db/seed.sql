-- E2E test fixture data with deterministic UUIDs

INSERT INTO organizations (id, name, created_at)
VALUES ('00000000-0000-0000-0000-000000000001', 'e2e-org', NOW());

INSERT INTO clusters (id, organization_id, name, agent_token_hash, created_at)
VALUES (
    '00000000-0000-0000-0000-000000000005',
    '00000000-0000-0000-0000-000000000001',
    'e2e-kind-cluster',
    -- SHA-256 of "e2e-test-token"
    '01a25bf06682c9f3179cd072e4095794cf1f344ae84bc6adf1b4a52212393e26',
    NOW()
);

INSERT INTO projects (id, organization_id, name, created_at)
VALUES (
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000001',
    'e2e-project',
    NOW()
);

INSERT INTO environments (id, project_id, name, namespace, default_ingress_base_host, cluster_id, created_at)
VALUES (
    '00000000-0000-0000-0000-000000000010',
    '00000000-0000-0000-0000-000000000002',
    'e2e-env',
    'e2e-test',
    'e2e.local',
    '00000000-0000-0000-0000-000000000005',
    NOW()
);

INSERT INTO services (id, project_id, name, created_at)
VALUES (
    '00000000-0000-0000-0000-000000000020',
    '00000000-0000-0000-0000-000000000002',
    'todo-app',
    NOW()
);

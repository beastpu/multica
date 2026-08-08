CREATE UNIQUE INDEX CONCURRENTLY workflow_role_assignment_instance_role_uidx
    ON workflow_instance_role_assignment (workflow_instance_id, role_key);

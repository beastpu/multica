-- applies_to was retired from the workflow definition schema. Remove it from
-- historical versions so runtime parsing can remain strict.
UPDATE workflow_version
SET definition = definition - 'applies_to',
    definition_checksum = ''
WHERE definition ? 'applies_to';

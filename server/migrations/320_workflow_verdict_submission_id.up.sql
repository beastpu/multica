-- Record which revision a verdict judged as a column, not inside `basis`.
--
-- The reviewed submission has always been written, buried in a JSONB blob
-- alongside unrelated keys. Nothing could query it, nothing constrained it, and
-- nothing noticed when the recorder attributed a verdict to whatever revision
-- happened to be newest at the time it landed rather than the one the reviewer
-- was given.
--
-- Deliberately not unique per (node, submission): a node legitimately collects
-- several verdicts on one revision — `blocked` while a reviewer cannot judge,
-- then `pass` once it can — and a constraint would refuse the second.
ALTER TABLE workflow_node_verdict
    ADD COLUMN IF NOT EXISTS submission_id UUID;

UPDATE workflow_node_verdict
SET submission_id = (basis->>'submission_id')::uuid
WHERE submission_id IS NULL
  AND basis ? 'submission_id'
  AND basis->>'submission_id' ~ '^[0-9a-fA-F-]{36}$';

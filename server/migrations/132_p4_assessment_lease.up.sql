-- Batch assessment worker lease. A worker task pulls pending assessments via
-- GET /api/operations/assessments/pending, which marks each returned row
-- running and stamps leased_until; a lease that expires without a submitted
-- result returns the row to the pending pool. NULL means not leased.
ALTER TABLE agent_fix_p4_assessment
  ADD COLUMN leased_until timestamptz;

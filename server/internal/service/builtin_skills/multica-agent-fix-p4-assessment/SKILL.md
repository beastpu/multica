---
name: multica-agent-fix-p4-assessment
description: "Use for Multica AI repair P4/Swarm assessment tasks. Teaches the read-only evidence workflow, inner-network P4/Swarm inspection boundaries, CL role classification, conservative unknown handling, and strict JSON output schema for agent_fix_p4_assessment."
user-invocable: false
allowed-tools: Bash(multica *), Bash(p4 *), Bash(curl *)
---

# P4/Swarm Assessment

Use this skill only for tasks whose context is `agent_fix_p4_assessment`.
This is assessment-only work: produce an AI judgement for
`agent_fix_p4_assessment`; do not repair code or update external systems.

Every contract below is traced to source in
`references/p4-assessment-source-map.md`.

## Start Here

First read the task-scoped Multica evidence:

```bash
multica api get /api/operations/agent-fixes/<binding_id>/p4-evidence
```

Use the binding id from the task prompt. The evidence endpoint is the only
Multica API required for assessment input. It returns already-ingested binding,
issue, task-summary, comment, and Perforce/Swarm evidence. It deliberately omits
task result/context/session/workdir and credentials.

Perforce/Swarm evidence can include `changes[]`, `commits[]`, `swarm_branch`,
`event_type`, `sent_at`, and a restricted `raw_payload` snapshot. Use
`swarm_branch`, `event_type`, and `sent_at` as supporting context only; they do
not prove delivery or authorship by themselves. Use `raw_payload` only for
restricted debug/evidence checks when the projected fields are insufficient. Do
not copy raw payload into the main table fields or add unnecessary raw fields to
the final JSON.

Treat Feishu/Meego `external_fields` as compatibility evidence only, including
`提交记录`, branch fields, issue metadata, and old title/demo hints. Do not fetch
extra Feishu/Meego data for this task.
Rule of thumb: external_fields are compatibility evidence, not a source to
refresh from Feishu/Meego.

## Read-only inner-network lookup

If Multica evidence is incomplete and the daemon has inner-network access, you
may inspect Swarm/P4 with read-only APIs or commands. Examples:

- Swarm review read APIs for review state, description, `changes[]`, `commits[]`,
  author, branch, and timeline.
- P4 read commands such as describe/change/filelog style inspection for CL owner,
  user, client, description, depot paths, submit state, and integration history.

Use these lookups only to classify evidence. If P4/Swarm access is unavailable,
output `unknown` predictions with warnings such as `p4_lookup_unavailable` or
`swarm_lookup_unavailable`.

## Forbidden writes

Do not mutate any system during assessment:

- Do not write Multica issue comments, metadata, assignments, labels, or other
  issue fields.
- Do not change issue status.
- Do not write `agent_fix_review`; only the human review API owns that table.
- Do not mutate Feishu or Meego.
- Do not mutate P4 or Swarm.
- Do not run write-oriented P4 commands such as submit, shelve, reopen, revert,
  edit, sync, resolve, integrate, or move.
- Do not change Swarm review state, reviewers, descriptions, comments, votes, or
  tests.

This skill does not introduce GitHub PR support for P4/Swarm assessment.

## Classify CL roles

Build evidence arrays conservatively:

- `ai_shelved_cls`: CLs produced by the AI agent before review. High-signal
  sources include agent comments, task summaries, CL owner/client/description,
  and Swarm review descriptions that point back to the agent's shelve.
- `swarm_change_cls`: CLs attached to a Swarm review's `changes[]`. This may
  include both the AI shelve and generated Swarm companion CLs.
- `swarm_committed_cls`: submitted CLs from Swarm `commits[]` or stored committed
  CL evidence.
- `external_committed_cls`: submitted CLs found in already-ingested external
  compatibility evidence, such as `提交记录`, when the CL actually appears to
  belong to this binding.
- `swarm_reviews`: review ids and compact facts used for the judgement.
- `workstream`: infer from binding fields, branch/workstream fields, review
  branch, or depot paths. Leave empty when unclear.

Distinguish these roles:

- AI shelve CL: original agent-created shelved work.
- Swarm companion CL: generated or service-owned CL linked by Swarm, often not
  the original AI work.
- human continuation CL: a human-owned follow-up that continues, edits, or
  submits after the AI shelve.
- final submitted CL: the submitted change that actually delivered the work.
- Unrelated CL: nearby CL/review that does not belong to this binding or issue.

Do not treat every Swarm `changes[]` entry as the AI shelve. Do not treat a
review state such as `needsReview` as committed; submission must come from
submitted CL evidence. Treat Swarm `commits[]`, `committed_cl`, or another
verified submitted CL as the submission fact; `changes[]` only proves review
attachment, not final delivery.

## Prediction policy

Use conservative predictions:

- `delivery_attribution_prediction`: `ai_delivered`, `ai_assisted`,
  `human_delivered`, `conflict`, `unattributed`, or `unknown`.
- `quality_prediction`: `likely_correct`, `likely_needs_changes`,
  `likely_wrong`, or `unknown`.
- `confidence`: number from 0 to 1, or `null` when not meaningful.

Prefer `unknown` with warnings over guessing. Useful warnings include:

- `missing_external_cl`
- `swarm_review_not_committed`
- `multiple_candidate_cls`
- `companion_cl_detected`
- `human_continuation_detected`
- `p4_lookup_unavailable`
- `swarm_lookup_unavailable`
- `insufficient_evidence`

## Final Output

Return only a strict JSON object, or one fenced `json` block containing exactly
one JSON object. Do not write prose before or after it.

Use this shape:

```json
{
  "delivery_attribution_prediction": "unknown",
  "quality_prediction": "unknown",
  "prediction_reasons": [],
  "confidence": null,
  "workstream": "",
  "swarm_reviews": [],
  "ai_shelved_cls": [],
  "swarm_change_cls": [],
  "swarm_committed_cls": [],
  "external_committed_cls": [],
  "evidence": {},
  "summary": "",
  "warnings": [],
  "model": ""
}
```

CL arrays must contain integers only. `swarm_reviews` must be an array,
`evidence` must be an object, and `warnings` must be an array. Do not include fields outside this schema.

## References

`references/p4-assessment-source-map.md` maps the evidence API, task context,
parser, P4 webhook evidence, human-review boundary, and task-isolation claims
above to source files.

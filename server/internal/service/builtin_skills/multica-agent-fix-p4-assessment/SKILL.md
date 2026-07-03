---
name: multica-agent-fix-p4-assessment
description: "Use for Multica AI repair P4/Swarm assessment work — the batch worker loop (pull pending assessments, judge each, submit by ref) and legacy per-binding agent_fix_p4_assessment tasks. Teaches the read-only evidence workflow, inner-network P4/Swarm inspection boundaries, CL role classification, conservative unknown handling, and the strict JSON result schema."
user-invocable: false
allowed-tools: Bash(multica *), Bash(p4 *), Bash(curl *)
---

# P4/Swarm Assessment

Use this skill when the task asks you to run P4/Swarm assessment — either a
batch worker run ("process the pending assessment queue") or a legacy task
whose context is `agent_fix_p4_assessment`. This is assessment-only work:
produce AI judgements; do not repair code or update external systems.

Every contract below is traced to source in
`references/p4-assessment-source-map.md`.

## Start Here — the batch loop

Pull a batch of pending assessments:

```bash
multica api get "/api/operations/assessments/pending?limit=5"
```

The response is `{"items": [...]}` where each item is:

```json
{ "ref": "<opaque>", "issue": {"title": "..."}, "lease_expires_at": "...", "evidence": {...} }
```

- `ref` is an OPAQUE handle. Echo it back on submit exactly as given — never
  construct, guess, or transform a ref, and never treat it as a binding id.
- `evidence` is inlined per item — no follow-up evidence call is needed.
- Each item is leased to your task until `lease_expires_at` (~30 minutes).
  Submit before then or the item silently returns to the pending pool.
- An empty `items` array means the queue is drained: report how many you
  assessed and stop.
- A `403` means this workspace is not allowlisted for P4 assessment — stop and
  report; do not retry.

For each item: classify the evidence (sections below), then submit:

```bash
multica api post /api/operations/assessments/result --content-file result.json
```

where `result.json` is the result schema (see "Submit the Result") plus the
`"ref"` field carried over from the item. On a `400`, fix exactly the field
the error names and resubmit. On a `409` your lease was reclaimed — drop that
item and continue; it will come back in a later pull. Loop pull → assess →
submit until a pull returns no items.

### Legacy per-binding tasks

A task whose context carries a `feishu_binding_id` predates the batch loop.
For those, read evidence with
`multica api get /api/operations/agent-fixes/<binding_id>/p4-evidence` and
submit to
`multica api post /api/operations/agent-fixes/<binding_id>/p4-assessment/result`
with the same result schema (no `ref` field). Everything else in this skill
applies unchanged.

## Reading the evidence

The evidence object contains already-ingested binding, issue, task-summary,
Multica comment, selected Feishu Project comment, and Perforce/Swarm evidence.
It deliberately omits task result/context/session/workdir and credentials.

Start from the structured candidates when present:

- `cl_candidates[]`: CL numbers extracted by the server from issue metadata
  such as `flow_cl`, external fields, Multica comments, Feishu Project
  comments when available, and linked Perforce/Swarm review rows.
- `review_candidates[]`: Swarm review ids extracted by the server from
  `flow_review`, external fields, Multica comments, Feishu Project comments
  when available, and linked review rows.

Candidate entries are hints, not proof. Use their `source` and `field` values
to prioritize lookup, then verify with read-only P4/Swarm evidence before
classifying final output.

Perforce/Swarm evidence can include `changes[]`, `commits[]`, `swarm_branch`,
`event_type`, `sent_at`, and a restricted `raw_payload` snapshot. Use
`swarm_branch`, `event_type`, and `sent_at` as supporting context only; they do
not prove delivery or authorship by themselves. Use `raw_payload` only for
restricted debug/evidence checks when the projected fields are insufficient. Do
not copy raw payload into the main table fields or add unnecessary raw fields to
the final JSON.

Treat Feishu/Meego `external_fields` as compatibility evidence only, including
`提交记录`, branch fields, and old title/demo hints. Do not fetch extra
Feishu/Meego data for this task.
Rule of thumb: external_fields are compatibility evidence, not a source to
refresh from Feishu/Meego.

Feishu/Meego availability is intentionally narrow:

- `binding.external_status_label` and `binding.mapped_status` come from the
  latest Multica sync.
- `issue.description` is the synced Multica issue body and may contain the
  original Feishu work item text and attachments.
- `comments[]` are Multica issue comments, not Feishu/Meego comments.
- `external_comments[]` are Feishu Project comments fetched by the Multica
  server through the configured integration plugin. This includes comments on
  the bound work item and, when the bound bug has `_field_linked_story`, comments
  on that linked story/requirement so hotfix flows whose CL comments live on the
  requirement can still be assessed. They can be empty because the Feishu API
  only returns OpenAPI-domain comments and omits UI-only system notification
  cards.
- `external_evidence_errors[]`, when present, records why optional external
  evidence could not be fetched. Treat missing external comments as
  insufficient evidence, not as proof that no CL exists.
- `external_fields` may be `{}` on historical rows or when the configured
  field whitelist did not include the needed field at sync time.
- Do not call Feishu/Meego APIs or assume a Feishu plugin token is available.
  If final CL evidence only exists in unavailable Feishu activity, return
  `unknown` with `missing_external_cl` or `insufficient_evidence`.

## Read-only inner-network lookup

If Multica evidence is incomplete and the daemon has inner-network access, you
may inspect Swarm/P4 with read-only APIs or commands.

Extract CL and review candidates from `cl_candidates[]` and
`review_candidates[]` first, then cross-check issue title/description, Multica
comments, `external_fields`, and `perforce_reviews[]`. Then use only read-only
probes such as:

```bash
p4 describe -S -s <shelved_cl>
p4 describe -S -du <shelved_cl>
p4 describe -s <submitted_or_pending_cl>
p4 changes -s submitted //depot/path/...@<start>,@now
p4 filelog //depot/path/to/file
curl -fsS https://<swarm-host>/api/v9/reviews/<review_id>
```

Use `p4 describe -S` for shelved CLs. `p4 opened -c <cl>` can say no files are
opened even when a shelved CL exists, so do not treat it as proof that the CL is
missing.

Swarm read APIs may return `Unauthorized` or an HTML login page when the runtime
has no Swarm credential. In that case, do not retry with write actions or scrape
the UI as fact; keep any review id found in evidence/comments, rely on P4
describe where possible, and add `swarm_lookup_unavailable`.

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

If the only verified implementation evidence is a pending shelved CL or a Swarm
review with no `commits[]` / submitted CL, the delivery is not proven. Record the
AI shelve and review evidence, but keep `quality_prediction` conservative and
add warnings such as `swarm_review_not_committed` and `missing_external_cl`.

## Compare Implementations

When both AI-side implementation evidence and final submitted CL evidence exist,
compare them before judging attribution and quality:

1. Resolve AI-side implementation evidence from `ai_shelved_cls`,
   `swarm_change_cls`, review descriptions, agent comments, and task summaries.
2. Resolve final delivered evidence from `external_committed_cls`,
   `swarm_committed_cls`, Feishu Project comments, and submitted CL descriptions.
3. Prefer code diff comparison:
   - AI shelve: `p4 describe -S -du <ai_shelved_cl>`
   - submitted CL: `p4 describe -du <submitted_cl>`
4. If the AI shelved diff is unavailable or empty, compare the AI plan/CL
   description/review description against the submitted CL description and diff.

Record exactly one binary method-equivalence result inside
`evidence.implementation_comparison` with compact facts such as:

- `ai_sources`: CLs/comments/reviews used as the AI-side baseline.
- `final_sources`: submitted CLs/comments used as the delivered baseline.
- `method_equivalence`: `equivalent` or `not_equivalent`.
- `reason`: one short evidence-backed reason for the binary judgement.
- `notable_differences`: short, evidence-backed differences only.

There is no middle state for method equivalence:

- `equivalent` means the AI-side implementation and the final submitted CL use
  the same bug-fix method or an equivalent method, even if function names,
  variable names, file integration mechanics, or manual merge details differ.
- `not_equivalent` means the final submitted CL uses a materially different fix
  method, there is no meaningful overlap with the AI-side implementation, or
  the available evidence is insufficient to prove equivalence.

Use the comparison to classify delivery and quality:

- `method_equivalence: "equivalent"` normally maps to
  `quality_prediction: "likely_correct"` for the implementation-comparison
  dimension, unless independent evidence proves the submitted fix is wrong.
- `method_equivalence: "not_equivalent"` maps to
  `quality_prediction: "likely_wrong"` for the AI assessment dimension.
- If the final submitted CL was submitted by Multica/automation or a Multica
  issue comment records the final submitted CL, use that evidence when choosing
  `delivery_attribution_prediction`.
- If a human manually submitted a CL that is method-equivalent to the AI/Swarm
  work, prefer `ai_assisted`. If automation submitted the equivalent final CL,
  prefer `ai_delivered`.
- If a human submitted a non-equivalent final CL, prefer `human_delivered` or
  `unattributed` depending on whether the human ownership is proven.

## Prediction policy

Use conservative predictions:

- `delivery_attribution_prediction`: `ai_delivered`, `ai_assisted`,
  `human_delivered`, `conflict`, `unattributed`, or `unknown`.
- `quality_prediction`: `likely_correct`, `likely_needs_changes`,
  `likely_wrong`, or `unknown`.
- `confidence`: number from 0 to 1, or `null` when not meaningful.

`quality_prediction` judges exactly one thing: the AI-side solution measured
against the final delivery (the implementation comparison above). It is never
a grade of a human-authored fix. When there is no verified AI output evidence
(no AI shelve CL and no Swarm review), or the AI output evidence could not be
accessed, output `quality_prediction: "unknown"` — do not judge the human fix
in its place. The operations dashboard computes the AI fix rate only from
tickets whose AI output evidence was reachable, so a wrongly-graded human fix
corrupts the metric.

Prefer `unknown` with warnings over guessing. Useful warnings include:

- `missing_external_cl`
- `swarm_review_not_committed`
- `multiple_candidate_cls`
- `companion_cl_detected`
- `human_continuation_detected`
- `p4_lookup_unavailable`
- `swarm_lookup_unavailable`
- `insufficient_evidence`

When evidence access is blocked, the warnings array MUST contain at least one
of these canonical values so the dashboard can classify the block:

- `p4_lookup_unavailable` — P4 unreachable, or a claimed shelve/CL could not
  be verified from this runtime.
- `swarm_lookup_unavailable` — Swarm API unreachable or unauthorized.
- `evidence_endpoint_unavailable` — the Multica evidence endpoint itself
  failed.

You may add more specific detail warnings alongside the canonical one (e.g.
`claimed_shelved_cl_not_found_on_reachable_p4`), but never replace it: a
result whose only block signal is a free-form variant is counted as
verifiable by the dashboard and skews the fix rate.

## Submit the Result

Submit each result by POSTing JSON to the batch result endpoint — this is how
the assessment reaches the operations dashboard. Do NOT rely on printing the
JSON as your final message; the endpoint is the authoritative path. Write the
JSON to a file and post it with `--content-file` so shell quoting can't
corrupt it:

```bash
multica api post /api/operations/assessments/result --content-file result.json
```

The request body is exactly one JSON object with this shape (no surrounding
prose, no envelope):

```json
{
  "ref": "<echoed from the pending item>",
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
`evidence` must be an object, and `warnings` must be an array.
Do not include fields outside this schema. (Legacy per-binding tasks POST the
same body WITHOUT `ref` to
`/api/operations/agent-fixes/<binding_id>/p4-assessment/result`.)

On success the endpoint returns `{"status":"completed"}`. On a `400` it returns
the exact validation problem (e.g. `invalid quality_prediction`,
`confidence out of range`, an unknown field name) — read it, fix that field,
and POST again until it succeeds. On a `409` the lease was reclaimed by
another worker — drop the item and move on; a result that never submits
successfully leaves the assessment unrecorded.

## References

`references/p4-assessment-source-map.md` maps the evidence API, task context,
parser, P4 webhook evidence, human-review boundary, and task-isolation claims
above to source files.

---
name: multica-agent-fix-p4-assessment
description: "Use for Multica AI repair P4/Swarm assessment work — you are assigned one assessment issue per run (task context carries the Feishu binding): read the task-scoped evidence, verify read-only against inner-network P4/Swarm, narrate on your own assessment issue, and POST the strict JSON result. Teaches the read-only boundaries, CL role classification, and conservative unknown handling."
user-invocable: false
allowed-tools: Bash(multica *), Bash(p4 *), Bash(curl *)
---

# P4/Swarm Assessment

Use this skill when your task is a P4/Swarm assessment run — the task context
type is `agent_fix_p4_assessment` and the task is assigned to a derived
**assessment issue** (one issue per run, created and status-managed by the
server). This is assessment-only work: produce AI judgements; do not repair
code or update external systems.

Every contract below is traced to source in
`references/p4-assessment-source-map.md`.

## Start Here — one run, one assessment issue

You were assigned an assessment issue. The real defect issue is referenced in
the evidence and is READ-ONLY for you; everything you write goes to your own
assessment issue or the result endpoint.

All server calls are plain HTTP with `curl` — do NOT depend on any `multica`
CLI subcommand (installed CLI versions vary and may lack newer commands). The
daemon already injects everything you need into the task environment:
`MULTICA_SERVER_URL`, `MULTICA_TOKEN` (task-scoped `mat_` token),
`MULTICA_WORKSPACE_ID`, `MULTICA_AGENT_ID`, `MULTICA_TASK_ID`. Never print
`MULTICA_TOKEN`.

Your task context carries the Feishu/Meego binding id (`feishu_binding_id`,
also echoed in your opening prompt). Read the task-scoped evidence:

```bash
status=$(curl -sS -o /tmp/evidence.json -w "%{http_code}" \
  "${MULTICA_SERVER_URL%/}/api/operations/agent-fixes/<binding_id>/p4-evidence" \
  -H "Authorization: Bearer $MULTICA_TOKEN" \
  -H "X-Workspace-ID: $MULTICA_WORKSPACE_ID" \
  -H "X-Agent-ID: $MULTICA_AGENT_ID" \
  -H "X-Task-ID: $MULTICA_TASK_ID")
echo "$status"; cat /tmp/evidence.json
```

- Evidence access is task-scoped: a `403` means this task is not the binding's
  assessment task (or the workspace/binding in your task context does not
  match) — stop and report; do not retry with other ids.
- The workflow is: read evidence → classify (sections below) → optionally
  narrate on your assessment issue → POST the result JSON. One run assesses
  exactly one binding; there is no queue to pull or loop over.

### Narrate on your assessment issue

Your assigned assessment issue exists to hold your process narration: you may
post plain comments there to record the evidence chain and key judgement steps
(which CLs you probed, what `p4 describe` showed, why you chose a prediction).
This is optional but recommended — it is what operators read when they ask
"what did the assessment actually check". Write narration comments in Chinese
(the operators are a Chinese-speaking team); keep technical identifiers — CL
numbers, commands, field names, enum values — verbatim.

```bash
curl -sS -X POST \
  "${MULTICA_SERVER_URL%/}/api/issues/<your_assessment_issue_id>/comments" \
  -H "Authorization: Bearer $MULTICA_TOKEN" \
  -H "X-Workspace-ID: $MULTICA_WORKSPACE_ID" \
  -H "X-Agent-ID: $MULTICA_AGENT_ID" \
  -H "X-Task-ID: $MULTICA_TASK_ID" \
  -H "Content-Type: application/json" \
  --data-binary '{"content": "verified shelved CL 12345 via p4 describe -S; diff matches submitted CL 12399"}'
```

Hard boundaries, server-enforced:

- Comment ONLY on your own assessment issue (the issue this task is assigned
  to). Never comment on the real defect issue (the `issue` in the evidence) or
  on any other assessment issue — the server rejects both with 403.
- Do NOT change the assessment issue's status, fields, or assignee — its
  status is projected by the server from the run lifecycle
  (todo → in_progress → done / cancelled); writes are rejected with 403.
- Narration never replaces the result submit. The POST to the result endpoint
  is the only way the assessment is recorded; the server also writes a
  structured result-summary comment on the assessment issue after a
  successful submit.

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
  issue fields. The ONLY exception is posting plain comments on YOUR OWN
  assessment issue (see "Narrate on your assessment issue"); everything else
  on that issue — status, fields, assignee, metadata — is still server-owned
  and rejected.
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
- `swarm_committed_cls`: submitted CLs verified as delivered — from Swarm
  `commits[]`, stored committed-CL evidence, OR a submitted CL you confirmed
  through read-only P4 inspection (`p4 describe -s <cl>` / `p4 filelog`) when no
  structured source carried it. If your P4 lookup finds the submitted CL that
  delivered the work (e.g. a shelve later submitted as an identical-diff CL),
  record that CL number here.
- `external_committed_cls`: submitted CLs found in already-ingested external
  compatibility evidence, such as `提交记录`, when the CL actually appears to
  belong to this binding.

Always write a verified submitted CL into one of these structured
`*_committed_cls` arrays — never leave it only in `summary` /
`prediction_reasons`. Delivery attribution and the operations delivery metrics
read the structured arrays, not the prose; a submitted CL you found in P4 but
narrated only in text is invisible to them and undercounts delivery (the
`structured_evidence_endpoint_unavailable` fallback path is exactly when this
happens).
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

One data-gap warning is equally contractual: when the external work item is
done but no submitted CL was found in ANY evidence source (external work-item
comments / 提交记录, Swarm `commits[]`, stored committed-CL evidence), the
warnings array MUST include `missing_external_cl`. The dashboard counts it as
the "missing human CL" process gap — the signal that the human who delivered
never recorded their final CL on the work item. Without the canonical value
that ticket is indistinguishable from an assessment that simply didn't look.

## Submit the Result

Submit the result by POSTing JSON to the binding's result endpoint — this is
how the assessment reaches the operations dashboard. Do NOT rely on printing
the JSON as your final message (that is only a parser fallback); the endpoint
is the authoritative path. Write the JSON to a file and post it so shell
quoting can't corrupt it:

```bash
status=$(curl -sS -o /tmp/submit.json -w "%{http_code}" -X POST \
  "${MULTICA_SERVER_URL%/}/api/operations/agent-fixes/<binding_id>/p4-assessment/result" \
  -H "Authorization: Bearer $MULTICA_TOKEN" \
  -H "X-Workspace-ID: $MULTICA_WORKSPACE_ID" \
  -H "X-Agent-ID: $MULTICA_AGENT_ID" \
  -H "X-Task-ID: $MULTICA_TASK_ID" \
  -H "Content-Type: application/json" \
  --data-binary @result.json)
echo "$status"; cat /tmp/submit.json
```

The request body is exactly one JSON object with this shape (no surrounding
prose, no envelope):

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
`evidence` must be an object, and `warnings` must be an array.
Do not include fields outside this schema.

Language rule: write `summary` and every entry of `prediction_reasons` in
Chinese — the operators reading the result comment are a Chinese-speaking
team. Keep technical identifiers verbatim inside the Chinese text: CL numbers,
commands, field names, and enum values (`human_delivered`, `likely_wrong`, …)
stay as-is. `warnings` entries are machine codes consumed by metrics — do NOT
translate them.

On success the endpoint returns `{"status":"completed"}` and the server
projects your assessment issue to done and writes the result-summary comment.
On a `400` it returns the exact validation problem (e.g.
`invalid quality_prediction`, `confidence out of range`, an unknown field
name) — read it, fix that field, and POST again until it succeeds. A result
that never submits successfully leaves the assessment unrecorded (the run is
then marked failed from the task outcome).

## References

`references/p4-assessment-source-map.md` maps the evidence API, task context,
parser, P4 webhook evidence, human-review boundary, and task-isolation claims
above to source files.

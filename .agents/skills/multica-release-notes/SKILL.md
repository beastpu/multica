---
name: multica-release-notes
description: Use when drafting, reviewing, creating, moving, previewing, or publishing Multica update notes / changelog announcements to Feishu docs and Feishu group cards, especially when combining official changelog entries with local GitLab repository commits.
---

# Multica Release Notes

Use this skill for Multica update notes, release notes, and Feishu announcement cards.

## Goal

Create a concise Feishu update document based on:

- Previous Multica update note formatting and location.
- The latest published update note date, metadata, and content when running in automation.
- Official Multica changelog: `https://multica.ai/changelog`.
- Local/internal GitLab repository changes and commits.

Then follow the review and publishing flow:

1. Create or update the Feishu document.
2. Move the document to the same Wiki/organization parent as previous update notes.
3. Ask the user to review the document content.
4. After review passes, send a Feishu card preview to the user only.
5. After the user confirms the preview, send the card to the target group.

Never send to the group before explicit user confirmation.

## Required Inputs

Prefer inferring these so the skill works in a fixed automation prompt. Ask only
when the missing value cannot be discovered from the available document/Wiki
context.

- Release date or title date, for example `2026-06-18`.
  - Default: today's date in the user's timezone, unless a current draft
    document title or metadata clearly provides a different date.
- Change range.
  - Default: from the previous published update note's timestamp/date to now.
  - If the previous note has both a title date and document update timestamp,
    use the later timestamp as the exclusive lower bound and mention this
    inference in the working notes.
- Previous update note URL or a stable Wiki parent/current note URL.
  - Use it for format reference, target Wiki parent, and automatic range
    discovery.
- Target group chat ID, only used after preview approval.
- Bot/profile to send the card.

Known defaults for the current Multica workflow:

- Official changelog: `https://multica.ai/changelog`
- Release notes Wiki parent: `https://lilithgames.feishu.cn/wiki/Z2xJw3QuaiBtSMkjYTJcRt9Nntc`
  - Title: `Multica 文档中心`
  - `space_id`: `7067946713897517057`
  - `node_token`: `Z2xJw3QuaiBtSMkjYTJcRt9Nntc`
  - Release-note children are direct child docx nodes under this parent, for
    example `Multica 更新说明 2026-06-18`.
- Notification bot profile: `multica-notify`
- Notification bot app id: `cli_aa8861f3c0bb9cde`
- Default publish group: `oc_3bc000be3a30aadd8ba8042d8c015799`

## Automation Mode

When the user asks for an automated or recurring release-note task, assume the
prompt should stay stable across runs. Do not require the user to edit dates,
commit ranges, or "previous note" wording every time.

Automation-friendly flow:

1. Start from the provided current update-note document, latest previous note
   URL, or Wiki parent. A Codex cron automation has no implicit "current
   Feishu document" context, so the automation prompt must include one stable
   Feishu Wiki parent URL or latest update note URL.
2. Read the current/latest document metadata and content:
   - document title;
   - created/updated timestamps if exposed by Feishu/Wiki/Drive metadata;
   - visible date in the title or first heading;
   - body content, to reuse structure and avoid repeating already-published
     items.
3. Determine the previous published note:
   - If a previous note URL is provided, use that document.
   - If only a Wiki parent/current note is provided, inspect sibling update
     notes under the same parent and pick the most recent published note before
     the current run.
   - Prefer documents whose title matches `Multica 更新说明`, `Multica Update`,
     `更新说明`, or a dated release-note pattern.
4. Set the change window:
   - `start`: previous note update timestamp when available; otherwise the date
     parsed from its title/heading/content.
   - `end`: current run time; if updating an existing draft, use the draft's
     latest update timestamp only as context, not as the upper bound.
   - Treat `start` as exclusive and `end` as inclusive for changelog and Git log
     collection.
5. Generate the new changelog from official changelog entries and local GitLab
   commits within that window.
6. Report the inferred `start`, `end`, previous note URL/title, and source of
   the dates before asking for document review.

If multiple candidate previous notes have the same date, choose the one with the
latest Feishu/Wiki update timestamp. If no reliable previous note can be found,
ask once for either the previous update note URL or the Wiki parent; do not ask
for a hand-written date range unless document discovery is impossible.

Stable automation prompt example:

```text
Use the multica-release-notes skill to draft the next Multica update note.
Use https://lilithgames.feishu.cn/wiki/Z2xJw3QuaiBtSMkjYTJcRt9Nntc as the
source of truth for previous notes and formatting. Infer the change range
automatically from the latest published note to the current run time. Create or
update the Feishu document, then stop and ask me to review before sending any
card.
```

Only this stable Wiki parent URL should need to stay in the automation prompt.
Dates and commit ranges should be discovered during the run.

Read locations:

- Feishu document body: use the `lark-doc` workflow on the resolved docx token.
- Feishu/Wiki node location and sibling notes: use the `lark-wiki` workflow to
  resolve the node, parent, and sibling update-note documents.
- Created/updated timestamps: prefer Feishu/Wiki/Drive metadata exposed for the
  document or Wiki node; fall back to the visible date in the document title or
  first heading only when metadata is unavailable.
- Product changes: read `https://multica.ai/changelog` and local Git history for
  the inferred `start`/`end` window.

## Content Rules

- Keep the document concise. Record core features and important fixes, not every commit.
- Match the style of previous update notes: clear headings, short bullets, and a polished top summary.
- Prefer product-facing wording over raw commit wording.
- Do not include uncertain features, unmerged work, or internal implementation details unless the user asks.
- When the user says a topic should be removed, remove it from both the document and the preview card.
- Treat screenshots as optional supporting visuals for feature sections when they clarify the update.

Common sections:

- `核心 Feature`
- `重点修复`

Useful feature categories for recent Multica notes:

- Custom Runtime / runtime profile / daemon recognition.
- Skills import, including Atlas SkillHub.
- Feishu Project / Meegle work item integration.
- Lark bot collaboration and Inbox notifications.
- Issue, Chat, Autopilot, and Squad collaboration improvements.

## Research Workflow

1. Resolve the date range.
   - In automation mode, derive it from the previous published update note and
     current run time as described above.
   - In manual mode, use the user-provided dates when explicit.
   - Keep a short note of the inferred range so the final answer can explain
     what was included.

2. Read the previous update note.
   - Use it as the format reference.
   - Read its body content to avoid duplicating already-published items.
   - Read document/Wiki/Drive metadata when available, especially created and
     updated timestamps.
   - Resolve its Wiki node metadata to find `space_id` and `parent_node_token`.

3. Read the official changelog.
   - Browse or fetch `https://multica.ai/changelog` because it changes over time.
   - Extract only relevant entries within the requested date range.

4. Inspect local/internal repository changes.
   - Check branches and remotes before assuming repository state.
   - Use `git log --after=<start> --until=<end>` and focused `git show` / `rg`
     to identify user-facing features and fixes.
   - Group related commits into product themes.

5. Reconcile sources.
   - Official changelog is the public baseline.
   - Local GitLab commits can add internal integrations, fixes, or not-yet-public work.
   - If a feature appears local-only or unmerged, call that out to the user before publishing it.

## Test Environment Update Scope

When the user asks to update the test environment as part of the release-note
workflow, first classify the change surface from the Git range before proposing
what to update.

- Backend-only: changed files are limited to backend/runtime/deploy surfaces such
  as `server/`, backend SQL/migrations, backend-only scripts, or backend image
  configuration. In this case, update only the test backend service/image; do
  not rebuild or redeploy the web/frontend test service just to publish the
  release note.
- Frontend-involved: changed files include `apps/web/`, `apps/desktop/`,
  `packages/core/`, `packages/ui/`, `packages/views/`, frontend package/config
  files, or other UI/client assets. In this case, include the relevant frontend
  test update in the plan.
- Mixed or uncertain: if the range includes both backend and frontend surfaces,
  or the impact is unclear from file paths, state the uncertainty and use the
  broader test update path unless the user confirms a narrower scope.

Report the inferred test update scope alongside the change range. If the scope
is backend-only, explicitly say that the test environment only needs the backend
updated.

## Feishu Document Workflow

Use `lark-doc`, `lark-wiki`, and `lark-drive` skills/CLI as needed.

1. Create a new document rather than appending to an old update note.
2. Populate content in the previous update note style.
3. Move it to the same Wiki parent as previous update notes:
   - Resolve previous note:
     ```bash
     lark-cli wiki spaces get_node --as user --params '{"token":"<previous_wiki_token>"}' --format json
     ```
   - Move the new docx into that parent:
     ```bash
     lark-cli wiki +move --as user \
       --obj-type docx \
       --obj-token <new_docx_token> \
       --target-space-id <space_id> \
       --target-parent-token <parent_node_token> \
       --format json
     ```
   - If the move is async, wait for completion or follow the returned `next_command`.
4. Give the user the Wiki URL and ask for review.

Do not proceed to card preview until the user says the document content is OK or gives concrete edits that have been applied.

## Feishu Card Workflow

After document review passes, send a preview card to the user only.

Card requirements:

- Title format: `Multica 更新说明 · YYYY-MM-DD`
- Include 4-6 concise sections from the document.
- Include one button: `查看完整更新说明`
- Button URL should be the final Wiki URL, not a temporary Drive doc URL.

Preferred preview path:

```bash
lark-cli api POST /open-apis/im/v1/messages \
  --profile multica-notify \
  --as bot \
  --params '{"receive_id_type":"email"}' \
  --data '{"receive_id":"<user_email>","msg_type":"interactive","content":"<escaped_card_json>"}' \
  --format json
```

If bot direct-message sending fails:

- Do not silently switch to the group.
- Explain the Feishu limitation.
- Ask whether to preview through another bot/profile or another personal channel.

After the user confirms the preview, send the same card to the target group:

```bash
lark-cli im +messages-send \
  --profile multica-notify \
  --as bot \
  --chat-id <target_group_chat_id> \
  --msg-type interactive \
  --content '<card_json>' \
  --format json
```

Report the final message ID.

## Safety Boundaries

- Never publish to a group until the user explicitly confirms the preview.
- Never expose app secrets, access tokens, cookies, or auth codes.
- If Feishu scope/auth is missing, use the lark split-flow authorization instructions.
- If using a different bot or group from the known defaults, restate the target before sending.
- For group sends, use the exact confirmed card content; do not make last-minute copy changes.

## Final Report

Include:

- Final document URL.
- Whether it was moved under the previous update note parent.
- Preview message ID, if sent.
- Group message ID, if published.
- Any caveats, such as features intentionally omitted or auth limitations.

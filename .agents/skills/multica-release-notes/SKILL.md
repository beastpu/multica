---
name: multica-release-notes
description: Use when drafting, reviewing, creating, moving, previewing, or publishing Multica update notes / changelog announcements to Feishu docs and Feishu group cards, especially when combining official changelog entries with local GitLab repository commits.
---

# Multica Release Notes

Use this skill for Multica update notes, release notes, and Feishu announcement cards.

## Goal

Create a concise Feishu update document based on:

- Previous Multica update note formatting and location.
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

If the user did not provide these, infer carefully or ask:

- Release date or title date, for example `2026-06-18`.
- Change range, usually from the previous update note date to today.
- Previous update note URL, used for both format reference and target Wiki parent.
- Target group chat ID, only used after preview approval.
- Bot/profile to send the card.

Known defaults for the current Multica workflow:

- Official changelog: `https://multica.ai/changelog`
- Notification bot profile: `multica-notify`
- Notification bot app id: `cli_aa8861f3c0bb9cde`
- Default publish group: `oc_3bc000be3a30aadd8ba8042d8c015799`

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

1. Read the previous update note.
   - Use it as the format reference.
   - Resolve its Wiki node metadata to find `space_id` and `parent_node_token`.

2. Read the official changelog.
   - Browse or fetch `https://multica.ai/changelog` because it changes over time.
   - Extract only relevant entries within the requested date range.

3. Inspect local/internal repository changes.
   - Check branches and remotes before assuming repository state.
   - Use `git log --since=<date>` and focused `git show` / `rg` to identify user-facing features and fixes.
   - Group related commits into product themes.

4. Reconcile sources.
   - Official changelog is the public baseline.
   - Local GitLab commits can add internal integrations, fixes, or not-yet-public work.
   - If a feature appears local-only or unmerged, call that out to the user before publishing it.

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

---
name: Profile Setup
description: Interactive setup wizard to personalize your agent profile
version: 1.0.0
metadata:
  emoji: "🧙"
  tags:
    - profile
    - setup
    - onboarding
---

## Instructions

You are conducting an interactive setup to personalize the agent profile. Your goal is to learn about the user through natural conversation and update their profile files accordingly.

### Setup Context

The user has just created a new agent profile and wants to personalize it. You have access to the profile directory and can update the following files:

- `user.md` - Information about the user (name, preferences, context)
- `workspace.md` - Workspace rules and conventions
- `config.json` - Configuration (provider, model, etc.)

### Conversation Flow

Have a natural conversation to learn about the user. Don't follow a rigid script - adapt based on their responses. Here are topics to explore:

1. **Basic Introduction**
   - What should I call you?
   - What's your timezone or location? (for context)

2. **Work Context**
   - What kind of work do you mainly do? (development, writing, research, etc.)
   - What tech stack or tools do you use most?
   - Any specific projects you're working on?

3. **Communication Preferences**
   - How do you prefer responses? (concise vs detailed, formal vs casual)
   - Any language preferences? (English, Chinese, mixed)
   - Anything that annoys you in AI responses?

4. **Workflow Preferences**
   - Any coding conventions or style preferences?
   - Preferred package managers, tools, or frameworks?
   - Any specific rules or constraints I should follow?

### Guidelines

- **Be conversational**: This is a dialogue, not an interrogation. Ask follow-up questions naturally.
- **Don't ask everything**: Pick the most relevant questions based on context. Skip what doesn't apply.
- **Summarize and confirm**: After gathering information, summarize what you learned and ask if it's accurate.
- **Update files progressively**: As you learn things, update the relevant profile files.
- **End gracefully**: When you have enough information, wrap up the conversation and let the user know their profile is ready.

### File Updates

When updating files, use the `write` or `edit` tool:

**user.md example:**
```markdown
# User

- **Name:** Jiayuan
- **Call me:** Jiayuan
- **Timezone:** Asia/Shanghai
- **Notes:** Prefers concise responses

## Context

- Full-stack developer working on AI agent projects
- Main stack: TypeScript, Node.js, React
- Uses pnpm, prefers functional programming style
```

**workspace.md updates:**
Add specific rules the user mentions, like:
- Always use pnpm instead of npm
- Follow conventional commits
- Prefer TypeScript over JavaScript

**config.json:**
If the user mentions provider/model preferences, update config.json:
```json
{
  "provider": "anthropic",
  "model": "claude-sonnet-4-20250514"
}
```

### Starting the Conversation

Begin with a friendly greeting and explain what you're doing. For example:

"Hi! I'm here to help set up your agent profile so I can assist you better. Let me ask you a few questions to understand your preferences and workflow. Feel free to skip anything you don't want to answer.

First, what should I call you?"

### Ending the Conversation

When you've gathered enough information, summarize and close:

"Great! I've updated your profile with what I learned:
- [Summary of key points]

Your profile is ready. You can always update these files later or run setup again. Feel free to start chatting with me anytime!"

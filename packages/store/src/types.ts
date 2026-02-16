import type { ContentBlock } from "@multica/sdk"

export type ToolStatus = "running" | "success" | "error" | "interrupted"

export type DelegateTaskStatus = "pending" | "running" | "success" | "error" | "timeout"

export interface DelegateTaskProgress {
  index: number
  label: string
  status: DelegateTaskStatus
  startedAtMs?: number
  durationMs?: number
  error?: string
}

export interface DelegateToolProgress {
  kind: "delegate_progress"
  taskCount: number
  completed: number
  running: number
  ok: number
  errors: number
  timeouts: number
  tasks: DelegateTaskProgress[]
  updatedAtMs: number
}

/** Message source: where did this message come from? */
export type MessageSource =
  | { type: "local" }
  | { type: "gateway"; deviceId: string }
  | { type: "channel"; channelId: string; accountId: string; conversationId: string }

export interface CompactionInfo {
  removed: number
  kept: number
  tokensRemoved?: number
  tokensKept?: number
  reason: string
}

export interface Message {
  id: string
  role: "user" | "assistant" | "toolResult" | "system"
  content: ContentBlock[]
  agentId: string
  stopReason?: string
  toolCallId?: string
  toolName?: string
  toolArgs?: Record<string, unknown>
  toolStatus?: ToolStatus
  toolProgress?: DelegateToolProgress
  isError?: boolean
  systemType?: "compaction"
  compaction?: CompactionInfo
  /** Message source (only for user messages) */
  source?: MessageSource
}

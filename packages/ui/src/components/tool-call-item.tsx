"use client"

import { memo, useEffect, useState } from "react"
import {
  File,
  Save,
  FileEdit,
  Terminal,
  Search,
  FolderOpen,
  Globe,
  Database,
  GitBranch,
  BarChart3,
  ChevronRight,
  type LucideIcon,
} from "lucide-react"
import { cn, getTextContent } from "@multica/ui/lib/utils"
import type { DelegateTaskProgress, DelegateTaskStatus, DelegateToolProgress, Message } from "@multica/store"

// ---------------------------------------------------------------------------
// Tool display config
// ---------------------------------------------------------------------------

const TOOL_DISPLAY: Record<string, { label: string; icon: LucideIcon }> = {
  read:            { label: "Read",          icon: File },
  write:           { label: "Write",         icon: Save },
  edit:            { label: "Edit",          icon: FileEdit },
  exec:            { label: "Exec",          icon: Terminal },
  bash:            { label: "Exec",          icon: Terminal },
  process:         { label: "Process",       icon: Terminal },
  grep:            { label: "Grep",          icon: Search },
  find:            { label: "Find",          icon: Search },
  ls:              { label: "ListDir",       icon: FolderOpen },
  glob:            { label: "Glob",          icon: Search },
  web_search:      { label: "WebSearch",     icon: Globe },
  web_fetch:       { label: "WebFetch",      icon: Globe },
  memory_get:      { label: "MemoryGet",     icon: Database },
  memory_set:      { label: "MemorySet",     icon: Database },
  memory_delete:   { label: "MemoryDelete",  icon: Database },
  memory_list:     { label: "MemoryList",    icon: Database },
  delegate:        { label: "Delegate",      icon: GitBranch },
  data:            { label: "Data",          icon: BarChart3 },
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Extract a short basename from a file path */
function basename(path: string): string {
  return path.split("/").pop() ?? path
}

/** Smart subtitle based on tool type and args */
function getSubtitle(toolName: string, args?: Record<string, unknown>): string {
  if (!args) return ""
  switch (toolName) {
    case "read":
    case "write":
    case "edit":
      return args.path ? basename(String(args.path)) : ""
    case "exec":
    case "bash":
    case "process": {
      const cmd = String(args.command ?? args.cmd ?? "")
      return cmd.length > 60 ? cmd.slice(0, 57) + "…" : cmd
    }
    case "grep":
    case "find":
      return args.pattern ? String(args.pattern) : ""
    case "glob":
      return args.pattern ? String(args.pattern) : ""
    case "web_search":
      return args.query ? String(args.query) : ""
    case "web_fetch":
      try { return new URL(String(args.url)).hostname } catch { return String(args.url ?? "") }
    case "data": {
      const action = String(args.action ?? "").replace(/^get_/, "")
      const params = args.params as Record<string, unknown> | undefined
      const ticker = params?.ticker ? String(params.ticker).toUpperCase() : ""
      return ticker ? `${action} ${ticker}` : action
    }
    case "delegate": {
      const tasks = args.tasks as Array<{ label?: string; task?: string }> | undefined
      if (!tasks?.length) return ""
      const labels = tasks.map((t, i) => t.label || `Task ${i + 1}`)
      const summary = labels.join(", ")
      return summary.length > 60 ? summary.slice(0, 57) + "…" : summary
    }
    default:
      return ""
  }
}

/** Running-state label per tool */
const RUNNING_LABELS: Record<string, string> = {
  read: "reading…",
  write: "writing…",
  edit: "editing…",
  exec: "running…",
  bash: "running…",
  process: "running…",
  grep: "searching…",
  find: "searching…",
  glob: "searching…",
  web_search: "searching…",
  web_fetch: "fetching…",
  data: "fetching…",
  delegate: "delegating…",
}

/** Stats derived from tool result content */
function getStats(toolName: string, toolStatus: string, resultText: string): string {
  if (toolStatus === "running") return RUNNING_LABELS[toolName] ?? "working…"
  if (toolStatus === "error" || toolStatus === "interrupted" || !resultText) return ""

  switch (toolName) {
    case "read": {
      const lines = resultText.split("\n").length
      return `${lines} lines`
    }
    case "grep": {
      const matches = resultText.split("\n").filter((l) => l.trim()).length
      return matches > 0 ? `${matches} matches` : ""
    }
    case "glob":
    case "find": {
      const files = resultText.split("\n").filter((l) => l.trim()).length
      return files > 0 ? `${files} files` : ""
    }
    default:
      return ""
  }
}

function getDelegateProgress(message: Message): DelegateToolProgress | undefined {
  const progress = message.toolProgress
  if (!progress) return undefined
  if (progress.kind !== "delegate_progress") return undefined
  return progress
}

function formatElapsed(ms?: number): string {
  if (typeof ms !== "number" || !Number.isFinite(ms) || ms <= 0) return ""
  const seconds = Math.round(ms / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remainingSeconds = seconds % 60
  return remainingSeconds > 0 ? `${minutes}m${remainingSeconds}s` : `${minutes}m`
}

function delegateTaskStatusLabel(task: DelegateTaskProgress, nowMs: number): string {
  const elapsed = task.status === "running"
    ? formatElapsed(
        typeof task.startedAtMs === "number" && Number.isFinite(task.startedAtMs)
          ? Math.max(0, nowMs - task.startedAtMs)
          : undefined,
      )
    : formatElapsed(task.durationMs)

  switch (task.status) {
    case "pending":
      return "pending"
    case "running":
      return "running"
    case "success":
      return elapsed ? `success · ${elapsed}` : "success"
    case "error":
      return elapsed ? `error · ${elapsed}` : "error"
    case "timeout":
      return elapsed ? `timeout · ${elapsed}` : "timeout"
    default:
      return task.status
  }
}

function delegateTaskStatusDotClass(status: DelegateTaskStatus): string {
  switch (status) {
    case "pending":
      return "bg-muted-foreground/40"
    case "running":
      return "bg-[var(--tool-running)] motion-safe:animate-pulse"
    case "success":
      return "bg-[var(--tool-success)]"
    case "error":
      return "bg-[var(--tool-error)]"
    case "timeout":
      return "bg-amber-500"
    default:
      return "bg-muted-foreground/40"
  }
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export const ToolCallItem = memo(function ToolCallItem({ message }: { message: Message }) {
  const [expanded, setExpanded] = useState(false)
  const [nowMs, setNowMs] = useState(() => Date.now())
  const { toolName = "", toolStatus = "running", toolArgs, content } = message
  const delegateProgress = toolName === "delegate" ? getDelegateProgress(message) : undefined
  const hasRunningDelegateTask = delegateProgress?.tasks.some((task) => task.status === "running") ?? false

  useEffect(() => {
    if (!hasRunningDelegateTask) return
    setNowMs(Date.now())
    const timer = globalThis.setInterval(() => {
      setNowMs(Date.now())
    }, 1000)
    return () => globalThis.clearInterval(timer)
  }, [hasRunningDelegateTask])

  const display = TOOL_DISPLAY[toolName] ?? { label: toolName, icon: Terminal }
  const isFinished = toolStatus !== "running"
  const resultText = getTextContent(content)
  const hasDetails = isFinished && !!resultText
  const subtitle = getSubtitle(toolName, toolArgs)
  const stats = getStats(toolName, toolStatus, resultText)

  return (
    <div className="py-0.5 px-2.5 text-sm text-muted-foreground">
      <div className={cn("rounded transition-colors", expanded && "bg-muted/30")}>
      <button
        type="button"
        aria-label={`${display.label}${subtitle ? ` ${subtitle}` : ""} — ${toolStatus}`}
        aria-expanded={hasDetails ? expanded : undefined}
        onClick={() => hasDetails && setExpanded(!expanded)}
        className={cn(
          "group flex w-full items-center gap-1.5 rounded px-2.5 py-1",
          "text-left transition-[color,background-color]",
          "focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 outline-none",
          hasDetails && !expanded && "hover:bg-muted/30 cursor-pointer",
          hasDetails && expanded && "cursor-pointer",
          !hasDetails && "cursor-default",
        )}
      >
        {/* Status dot */}
        <span
          className={cn(
            "size-1.5 rounded-full shrink-0",
            toolStatus === "running" && "bg-[var(--tool-running)] motion-safe:animate-[glow-pulse_2s_ease-in-out_infinite]",
            toolStatus === "success" && "bg-[var(--tool-success)]",
            toolStatus === "error" && "bg-[var(--tool-error)]",
            toolStatus === "interrupted" && "bg-[var(--tool-error)]",
          )}
        />

        {/* Tool icon */}
        <display.icon
          className={cn("size-3.5 shrink-0", toolStatus === "error" && "text-[var(--tool-error)]")}
        />

        {/* Tool label */}
        <span className={cn(
          "font-medium shrink-0",
          toolStatus === "error" && "text-[var(--tool-error)]",
          toolStatus === "interrupted" && "text-[var(--tool-error)]",
        )}>
          {display.label}
        </span>

        {/* Smart subtitle */}
        {subtitle && (
          <span className="text-muted-foreground/60 truncate min-w-0">
            {subtitle}
          </span>
        )}

        {/* Right-aligned stats */}
        {stats && (
          <span className={cn(
            "ml-auto text-xs text-muted-foreground/60 shrink-0",
            "font-[tabular-nums]",
            toolStatus === "running" && "motion-safe:animate-pulse",
          )}>
            {stats}
          </span>
        )}

        {/* Chevron — visible on hover when expandable */}
        {hasDetails && (
          <ChevronRight
            className={cn(
              "size-3 text-muted-foreground/40 shrink-0",
              "transition-[transform,opacity] duration-150",
              !stats && "ml-auto",
              "opacity-0 group-hover:opacity-100",
              expanded && "rotate-90 opacity-100",
            )}
          />
        )}
      </button>

      {/* Delegate task statuses */}
      {delegateProgress && delegateProgress.tasks.length > 0 && (
        <div className="px-2.5 pb-2">
          <div className="px-2.5 py-1 text-xs text-muted-foreground/70 font-[tabular-nums]">
            {delegateProgress.completed}/{delegateProgress.taskCount} completed
            {" · "}
            {delegateProgress.running} running
            {" · "}
            {delegateProgress.errors} failed
            {" · "}
            {delegateProgress.timeouts} timed out
          </div>
          <div className="space-y-0.5 px-2.5">
            {delegateProgress.tasks.map((task) => (
              <div key={`delegate-task-${task.index}`} className="flex items-center gap-2 text-xs">
                <span className={cn("size-1.5 rounded-full shrink-0", delegateTaskStatusDotClass(task.status))} />
                <span className="truncate min-w-0">{task.label}</span>
                <span className={cn(
                  "ml-auto shrink-0 text-muted-foreground/70 font-[tabular-nums]",
                  task.status === "running" && "motion-safe:animate-pulse",
                )}>
                  {delegateTaskStatusLabel(task, nowMs)}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Expanded result */}
      {expanded && resultText && (
        <div
          role="region"
          aria-label={`${display.label} result`}
          tabIndex={0}
          className="px-2.5 pt-1 pb-2 text-xs max-h-48 overflow-y-auto whitespace-pre-wrap break-all"
        >
          {resultText}
        </div>
      )}
      </div>
    </div>
  )
})

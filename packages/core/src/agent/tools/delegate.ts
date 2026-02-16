/**
 * delegate tool — run tasks in parallel via sub-agents.
 *
 * Synchronous from the LLM's perspective: the tool blocks until all
 * sub-agent tasks complete (or timeout), then returns combined results
 * directly in the tool response. No async infrastructure needed.
 */

import { join } from "node:path";
import { rmSync } from "node:fs";
import { Writable } from "node:stream";
import { Type } from "@sinclair/typebox";
import type { AgentTool } from "@mariozechner/pi-agent-core";
import { Agent } from "../runner.js";
import type { RunLog } from "../run-log.js";
import { DATA_DIR } from "@multica/utils";

const TaskItemSchema = Type.Object({
  task: Type.String({ description: "The task for the sub-agent to perform.", minLength: 1 }),
  label: Type.Optional(
    Type.String({ description: "Short human-readable label for this task (used in output headers)." }),
  ),
});

const DelegateSchema = Type.Object({
  tasks: Type.Array(TaskItemSchema, {
    description: "One or more tasks to run in parallel. Each spawns an isolated sub-agent.",
    minItems: 1,
  }),
  timeoutSeconds: Type.Optional(
    Type.Number({
      description:
        "Per-task timeout in seconds. Default: 1800 (30 min). " +
        "Set higher for complex tasks. The sub-agent is aborted if it exceeds this limit.",
      minimum: 0,
    }),
  ),
});

type DelegateArgs = {
  tasks: Array<{ task: string; label?: string }>;
  timeoutSeconds?: number;
};

type TaskResult = {
  index: number;
  label: string;
  status: "ok" | "error" | "timeout";
  durationMs: number;
  findings: string;
  error?: string;
};

type DelegateTaskProgressStatus = "pending" | "running" | "success" | "error" | "timeout";

type DelegateTaskProgress = {
  index: number;
  label: string;
  status: DelegateTaskProgressStatus;
  startedAtMs?: number;
  durationMs?: number;
  error?: string;
};

export type DelegateResult = {
  taskCount: number;
  ok: number;
  errors: number;
  timeouts: number;
  totalDurationMs: number;
  tasks: TaskResult[];
};

export type DelegateProgress = {
  kind: "delegate_progress";
  taskCount: number;
  completed: number;
  running: number;
  ok: number;
  errors: number;
  timeouts: number;
  tasks: DelegateTaskProgress[];
  updatedAtMs: number;
};

export type DelegateToolDetails = DelegateResult | DelegateProgress;

export interface CreateDelegateToolOptions {
  /** Whether the current agent is itself a subagent */
  isSubagent?: boolean;
  /** Session ID of the parent agent */
  sessionId?: string;
  /** Resolved provider ID (inherited by sub-agents) */
  provider?: string;
  /** Model override (inherited by sub-agents) */
  model?: string;
  /** API key (inherited by sub-agents) */
  apiKey?: string;
  /** Working directory (inherited by sub-agents) */
  cwd?: string;
  /** Run-log instance for emitting delegate events */
  runLog?: RunLog;
  /** Whether run-log is enabled (passed to child agents) */
  enableRunLog?: boolean;
}

const DEFAULT_TIMEOUT_SECONDS = 1800; // 30 minutes

function formatElapsed(ms: number): string {
  const totalSeconds = Math.round(ms / 1000);
  if (totalSeconds < 60) return `${totalSeconds}s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes < 60) return seconds > 0 ? `${minutes}m${seconds}s` : `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return remainingMinutes > 0 ? `${hours}h${remainingMinutes}m` : `${hours}h`;
}

/**
 * Run a single sub-agent task with timeout.
 */
async function runSubagentTask(
  taskDef: { task: string; label?: string },
  index: number,
  timeoutMs: number,
  parentOptions: CreateDelegateToolOptions,
  runLog?: RunLog,
  onTaskStateChange?: (task: DelegateTaskProgress) => void,
): Promise<TaskResult> {
  const label = taskDef.label || `Task ${index + 1}`;
  const start = Date.now();

  runLog?.log("delegate_task_start", {
    index,
    label,
    task: taskDef.task.slice(0, 200),
  });
  onTaskStateChange?.({
    index,
    label,
    status: "running",
    startedAtMs: start,
  });

  const childAgent = new Agent({
    provider: parentOptions.provider,
    model: parentOptions.model,
    apiKey: parentOptions.apiKey,
    cwd: parentOptions.cwd,
    isSubagent: true,
    enableSkills: false,
    compactionMode: "tokens",
    enableRunLog: parentOptions.enableRunLog,
    // Suppress stdout/stderr output from child agents
    logger: {
      stdout: new NullStream(),
      stderr: new NullStream(),
    },
  });

  try {
    let result: { text: string; error?: string };
    let timedOut = false;

    if (timeoutMs > 0) {
      // Race agent.run against timeout
      let timer: ReturnType<typeof setTimeout>;
      result = await Promise.race([
        childAgent.run(taskDef.task),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => {
            timedOut = true;
            childAgent.abort();
            reject(new Error("timeout"));
          }, timeoutMs);
        }),
      ]).catch((err) => {
        if (timedOut) {
          return { text: "", error: `Timed out after ${formatElapsed(timeoutMs)}` };
        }
        throw err;
      }).finally(() => {
        clearTimeout(timer);
      });
    } else {
      // No timeout
      result = await childAgent.run(taskDef.task);
    }

    const durationMs = Date.now() - start;
    const status = timedOut ? "timeout" : result.error ? "error" : "ok";

    const taskResult: TaskResult = {
      index,
      label,
      status,
      durationMs,
      findings: result.text || "(no output)",
      error: result.error || undefined,
    };

    runLog?.log("delegate_task_end", {
      index,
      label,
      status,
      duration_ms: durationMs,
      findings_chars: taskResult.findings.length,
      error: taskResult.error,
    });
    onTaskStateChange?.({
      index,
      label,
      status: status === "ok" ? "success" : status,
      startedAtMs: start,
      durationMs,
      error: taskResult.error,
    });

    return taskResult;
  } catch (err) {
    const durationMs = Date.now() - start;
    const message = err instanceof Error ? err.message : String(err);

    const taskResult: TaskResult = {
      index,
      label,
      status: "error",
      durationMs,
      findings: "",
      error: message,
    };

    runLog?.log("delegate_task_end", {
      index,
      label,
      status: "error",
      duration_ms: durationMs,
      findings_chars: 0,
      error: message,
    });
    onTaskStateChange?.({
      index,
      label,
      status: "error",
      startedAtMs: start,
      durationMs,
      error: message,
    });

    return taskResult;
  } finally {
    // Flush session writes before cleanup
    await childAgent.flushSession();

    // Clean up child session directory unless run-log is enabled
    if (!parentOptions.enableRunLog) {
      try {
        const sessionDir = join(DATA_DIR, "sessions", childAgent.sessionId);
        rmSync(sessionDir, { recursive: true, force: true });
      } catch {
        // Best-effort cleanup
      }
    }
  }
}

export function createDelegateTool(
  options: CreateDelegateToolOptions,
): AgentTool<typeof DelegateSchema, DelegateToolDetails> {
  return {
    name: "delegate",
    label: "Delegate Tasks",
    description:
      "Run one or more tasks in parallel via isolated sub-agents. " +
      "Each task gets its own agent with full tool access. " +
      "Results are returned directly when all tasks complete. " +
      "Use this for parallelizable work: multi-stock research, comparative analysis, " +
      "data collection from multiple sources, or any task that benefits from parallel execution.",
    parameters: DelegateSchema,
    execute: async (_toolCallId, args, _signal, onUpdate) => {
      const { tasks, timeoutSeconds } = args as DelegateArgs;
      const timeoutMs = (timeoutSeconds ?? DEFAULT_TIMEOUT_SECONDS) * 1000;

      // Guard: sub-agents cannot delegate
      if (options.isSubagent) {
        return {
          content: [{ type: "text", text: "Error: delegate is not allowed from sub-agent sessions." }],
          details: {
            taskCount: 0,
            ok: 0,
            errors: 1,
            timeouts: 0,
            totalDurationMs: 0,
            tasks: [],
          },
        };
      }

      const labels = tasks.map((t, i) => t.label || `Task ${i + 1}`);
      const progressTasks: DelegateTaskProgress[] = labels.map((label, index) => ({
        index,
        label,
        status: "pending",
      }));

      const emitProgress = () => {
        if (!onUpdate) return;
        const completed = progressTasks.filter((t) => t.status !== "pending" && t.status !== "running").length;
        const running = progressTasks.filter((t) => t.status === "running").length;
        const ok = progressTasks.filter((t) => t.status === "success").length;
        const errors = progressTasks.filter((t) => t.status === "error").length;
        const timeouts = progressTasks.filter((t) => t.status === "timeout").length;

        const snapshot: DelegateProgress = {
          kind: "delegate_progress",
          taskCount: tasks.length,
          completed,
          running,
          ok,
          errors,
          timeouts,
          tasks: progressTasks.map((task) => ({ ...task })),
          updatedAtMs: Date.now(),
        };

        onUpdate({
          content: [{
            type: "text",
            text: `Tasks: ${completed}/${tasks.length} completed (${ok} success, ${errors} failed, ${timeouts} timed out)`,
          }],
          details: snapshot,
        });
      };

      options.runLog?.log("delegate_start", {
        task_count: tasks.length,
        timeout_seconds: timeoutSeconds ?? DEFAULT_TIMEOUT_SECONDS,
        labels,
      });

      const totalStart = Date.now();

      // Run all tasks in parallel
      const results = await Promise.all(
        tasks.map((taskDef, index) =>
          runSubagentTask(taskDef, index, timeoutMs, options, options.runLog, (taskProgress) => {
            progressTasks[index] = {
              index: taskProgress.index,
              label: taskProgress.label,
              status: taskProgress.status,
              startedAtMs: taskProgress.startedAtMs,
              durationMs: taskProgress.durationMs,
              error: taskProgress.error,
            };
            emitProgress();
          }),
        ),
      );

      const totalDurationMs = Date.now() - totalStart;
      const ok = results.filter((r) => r.status === "ok").length;
      const errors = results.filter((r) => r.status === "error").length;
      const timeouts = results.filter((r) => r.status === "timeout").length;

      options.runLog?.log("delegate_end", {
        task_count: tasks.length,
        ok,
        errors,
        timeouts,
        total_duration_ms: totalDurationMs,
      });

      // Format combined response
      const statusLine =
        `All ${tasks.length} task(s) completed: ${ok} succeeded, ${errors} failed, ${timeouts} timed out.\n` +
        `Total wall time: ${formatElapsed(totalDurationMs)}`;

      const taskSections = results.map((r) => {
        const statusTag = r.status === "ok" ? "OK" : r.status.toUpperCase();
        const header = `--- Task ${r.index + 1}: "${r.label}" [${statusTag}] (${formatElapsed(r.durationMs)}) ---`;
        const body = r.status === "error" && r.error
          ? `Error: ${r.error}\n${r.findings || ""}`
          : r.findings;
        return `${header}\n${body}`;
      });

      const responseText = `${statusLine}\n\n${taskSections.join("\n\n")}`;

      return {
        content: [{ type: "text", text: responseText }],
        details: {
          taskCount: tasks.length,
          ok,
          errors,
          timeouts,
          totalDurationMs,
          tasks: results,
        },
      };
    },
  };
}

/**
 * Writable stream that discards all output.
 * Used to suppress child agent stdout/stderr.
 */
class NullStream extends Writable {
  _write(_chunk: any, _encoding: string, callback: () => void): void {
    callback();
  }
}

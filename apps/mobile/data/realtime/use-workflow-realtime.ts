/**
 * Screen-scoped Workflow realtime.
 *
 * Workflow WS payloads are intentionally opaque triggers today, so mobile
 * mirrors Web/Desktop's invalidation-only strategy. This avoids applying an
 * out-of-order event over a newer cached revision. The hook is mounted only
 * by the host-issue affordance and Workflow detail screen, keeping cellular
 * refetch fanout bounded to visible Workflow surfaces.
 */
import { useQueryClient } from "@tanstack/react-query";
import type { WSEventType } from "@multica/core/types";
import { workflowKeys } from "@/data/queries/workflows";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";

const WORKFLOW_EVENTS = [
  "workflow_template:created",
  "workflow_template:updated",
  "workflow_template:published",
  "workflow_instance:updated",
  "workflow_node:updated",
  "workflow_node_task:updated",
  "workflow_executor_resolution:updated",
  "workflow_submission:created",
  "workflow_verdict:created",
  "workflow_confirmation:updated",
  "workflow_acceptance:updated",
] as const satisfies readonly WSEventType[];

export function useWorkflowRealtime(enabled = true) {
  const queryClient = useQueryClient();
  useWSSubscriptions(
    (ws, wsId) => {
      if (!enabled) return [];
      const invalidate = () => {
        queryClient.invalidateQueries({ queryKey: workflowKeys.all(wsId) });
      };
      return [
        ...WORKFLOW_EVENTS.map((event) => ws.on(event, invalidate)),
        ws.onReconnect(invalidate),
      ];
    },
    [enabled, queryClient],
  );
}

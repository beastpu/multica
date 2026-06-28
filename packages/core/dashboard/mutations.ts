import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import type {
  TriggerAgentFixP4AssessmentRequest,
  UpdateAgentFixReviewRequest,
} from "../types";
import { dashboardKeys } from "./queries";

export function useUpdateAgentFixReview() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: ({
      issueId,
      data,
    }: {
      issueId: string;
      data: UpdateAgentFixReviewRequest;
    }) => api.updateAgentFixReview(issueId, data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: dashboardKeys.all(wsId) });
    },
  });
}

export function useTriggerAgentFixP4Assessment() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (data: TriggerAgentFixP4AssessmentRequest) =>
      api.triggerAgentFixP4Assessment(data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: dashboardKeys.all(wsId) });
    },
  });
}

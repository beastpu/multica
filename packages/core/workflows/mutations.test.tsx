/**
 * @vitest-environment jsdom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import {
  useConfirmWorkflowNode,
  useCreateWorkflow,
  useCreateWorkflowSubmission,
} from "./mutations";
import { workflowKeys } from "./queries";
import type { WorkflowNodeDetail, WorkflowSubmission } from "./types";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("../auth", () => ({
  useAuthStore: (
    selector: (state: { user: { id: string } }) => unknown,
  ) => selector({ user: { id: "user-1" } }),
}));

const NODE_KEY = workflowKeys.node("ws-1", "node-1");

function makeNodeDetail(): WorkflowNodeDetail {
  return {
    submissions: [],
    confirmations: [],
    tasks: [],
    verdicts: [],
    participants: [],
    executor_resolutions: [],
  } as unknown as WorkflowNodeDetail;
}

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        {children}
      </QueryClientProvider>
    );
  };
}

function deferred<T>() {
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((_resolve, rejectPromise) => {
    reject = rejectPromise;
  });
  return { promise, reject };
}

function submissionResponse(payload: Record<string, unknown>) {
  const submission: WorkflowSubmission = {
    id: "submission-1",
    workflow_node_instance_id: "node-1",
    revision: 1,
    status: "valid",
    payload,
    summary: "",
    evidence: [],
    submitted_by_type: "member",
    submitted_by_id: "user-1",
    source_issue_id: null,
    source_agent_run_id: null,
    created_at: "2026-07-24T00:00:00.000Z",
  };
  return { submission, validation_errors: [] };
}

describe("workflow optimistic mutations", () => {
  let queryClient: QueryClient;

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
    queryClient.setQueryData(NODE_KEY, makeNodeDetail());
  });

  afterEach(() => {
    queryClient.clear();
    vi.restoreAllMocks();
  });

  it("shows a submission immediately and restores the cache after failure", async () => {
    const request = deferred<never>();
    setApiInstance({
      createWorkflowSubmission: vi.fn(() => request.promise),
    } as unknown as ApiClient);
    const { result } = renderHook(
      () => useCreateWorkflowSubmission("instance-1", "node-1"),
      { wrapper: wrapper(queryClient) },
    );

    let mutation!: Promise<unknown>;
    act(() => {
      mutation = result.current.mutateAsync({ payload: { summary: "ready" } });
    });

    await waitFor(() => {
      const cached = queryClient.getQueryData<WorkflowNodeDetail>(NODE_KEY);
      expect(cached?.submissions).toHaveLength(1);
      expect(cached?.submissions[0]?.status).toBe("submitting");
    });

    request.reject(new Error("network unavailable"));
    await expect(mutation).rejects.toThrow("network unavailable");
    await waitFor(() => {
      expect(
        queryClient.getQueryData<WorkflowNodeDetail>(NODE_KEY)?.submissions,
      ).toEqual([]);
    });
  });

  it("shows the current member confirmation and restores it after failure", async () => {
    const request = deferred<never>();
    setApiInstance({
      confirmWorkflowNode: vi.fn(() => request.promise),
    } as unknown as ApiClient);
    const { result } = renderHook(
      () => useConfirmWorkflowNode("instance-1", "node-1"),
      { wrapper: wrapper(queryClient) },
    );

    let mutation!: Promise<unknown>;
    act(() => {
      mutation = result.current.mutateAsync({
        decision: "approved",
        comment: "looks good",
      });
    });

    await waitFor(() => {
      const confirmation = queryClient
        .getQueryData<WorkflowNodeDetail>(NODE_KEY)
        ?.confirmations[0];
      expect(confirmation?.member_id).toBe("user-1");
      expect(confirmation?.decision).toBe("approved");
      expect(confirmation?.comment).toBe("looks good");
    });

    request.reject(new Error("request rejected"));
    await expect(mutation).rejects.toThrow("request rejected");
    await waitFor(() => {
      expect(
        queryClient.getQueryData<WorkflowNodeDetail>(NODE_KEY)?.confirmations,
      ).toEqual([]);
    });
  });

  it("reuses an idempotency key after failure and rotates it after success", async () => {
    const createSubmission = vi.fn()
      .mockRejectedValueOnce(new Error("connection reset"))
      .mockResolvedValue(submissionResponse({ summary: "ready" }));
    setApiInstance({
      createWorkflowSubmission: createSubmission,
    } as unknown as ApiClient);
    const { result } = renderHook(
      () => useCreateWorkflowSubmission("instance-1", "node-1"),
      { wrapper: wrapper(queryClient) },
    );
    const input = { payload: { summary: "ready" } };

    await act(async () => {
      await expect(result.current.mutateAsync(input)).rejects.toThrow(
        "connection reset",
      );
    });
    await act(async () => {
      await result.current.mutateAsync(input);
    });
    await act(async () => {
      await result.current.mutateAsync(input);
    });

    const keys = createSubmission.mock.calls.map(
      ([, request]) => request.idempotency_key,
    );
    expect(keys[0]).toBe(keys[1]);
    expect(keys[2]).not.toBe(keys[1]);
  });

  it("keeps an atomic workflow create retry-safe when callers regenerate keys", async () => {
    const createWorkflow = vi.fn()
      .mockRejectedValueOnce(new Error("response lost"))
      .mockResolvedValue({
        instance: { id: "instance-1" },
        nodes: [],
        role_assignments: [],
      });
    setApiInstance({ createWorkflow } as unknown as ApiClient);
    const { result } = renderHook(() => useCreateWorkflow(), {
      wrapper: wrapper(queryClient),
    });
    const payload = {
      title: "Release workflow",
      template_id: "template-1",
      role_assignments: [],
    };

    await act(async () => {
      await expect(result.current.mutateAsync({
        ...payload,
        idempotency_key: "caller-key-1",
      })).rejects.toThrow("response lost");
    });
    await act(async () => {
      await result.current.mutateAsync({
        ...payload,
        idempotency_key: "caller-key-2",
      });
    });
    await act(async () => {
      await result.current.mutateAsync({
        ...payload,
        idempotency_key: "caller-key-3",
      });
    });

    const keys = createWorkflow.mock.calls.map(
      ([request]) => request.idempotency_key,
    );
    expect(keys[0]).toBe(keys[1]);
    expect(keys[2]).not.toBe(keys[1]);
  });
});

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { TakeoverButton } from "./takeover-button";

const mockState = vi.hoisted(() => ({
  mutateAsync: vi.fn(),
  isPending: false,
}));

vi.mock("@multica/core/issues/mutations", () => ({
  useTakeoverIssue: () => ({
    mutateAsync: mockState.mutateAsync,
    isPending: mockState.isPending,
  }),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      select: (dict: Record<string, Record<string, string>>) => string,
      vars?: Record<string, string>,
    ) => {
      const text = select({
        takeover: {
          action: "Take over",
          confirm_title: "Take over this issue?",
          confirm_body: "The agent will be stopped.",
          confirm: "Take over",
          cancel: "Cancel",
          failed: "Takeover failed",
          card_title: "Work scene handed over",
          card_from: "Taken over from {{agent}}.",
          card_body: "Continue from where the agent stopped:",
          card_empty: "No recorded work scene.",
          runtime: "Runtime",
          work_dir: "Working directory",
          session: "Agent session",
          session_hint: "Resume with claude --resume.",
          copy: "Copy",
          copied: "Copied",
        },
      });
      return vars
        ? text.replace(/\{\{(\w+)\}\}/g, (_, key: string) => vars[key] ?? "")
        : text;
    },
  }),
}));

describe("TakeoverButton", () => {
  beforeEach(() => {
    mockState.mutateAsync = vi.fn();
    mockState.isPending = false;
  });

  // Takeover cancels running work — it must never fire off a bare click.
  it("asks for confirmation before taking over", () => {
    render(<TakeoverButton issueId="issue-1" />);
    fireEvent.click(screen.getByRole("button", { name: "Take over" }));
    expect(screen.getByText("Take over this issue?")).toBeInTheDocument();
    expect(mockState.mutateAsync).not.toHaveBeenCalled();
  });

  // The card is the reason to call takeover instead of plain reassign: the
  // scene must surface right after the action, from the mutation response.
  it("shows the handed-over work scene after confirming", async () => {
    mockState.mutateAsync.mockResolvedValue({
      takeover: {
        from_agent: { id: "a1", name: "Dev Agent" },
        task_id: "t1",
        runtime: { id: "r1", name: "beast's MacBook" },
        work_dir: "ws-1/task-1/repo",
        session_id: "sess-9",
      },
    });
    render(<TakeoverButton issueId="issue-1" />);
    fireEvent.click(screen.getByRole("button", { name: "Take over" }));
    fireEvent.click(screen.getAllByRole("button", { name: "Take over" }).at(-1)!);

    await waitFor(() => {
      expect(screen.getByText("Work scene handed over")).toBeInTheDocument();
    });
    expect(mockState.mutateAsync).toHaveBeenCalledWith("issue-1");
    expect(screen.getByText("Taken over from Dev Agent.")).toBeInTheDocument();
    expect(screen.getByText("ws-1/task-1/repo")).toBeInTheDocument();
    expect(screen.getByText("sess-9")).toBeInTheDocument();
    expect(screen.getByText("beast's MacBook")).toBeInTheDocument();
  });

  // A card with nothing in it must still close the loop: the takeover
  // happened, there is just no scene to continue from.
  it("says so when the agent left no work scene", async () => {
    mockState.mutateAsync.mockResolvedValue({ takeover: null });
    render(<TakeoverButton issueId="issue-1" />);
    fireEvent.click(screen.getByRole("button", { name: "Take over" }));
    fireEvent.click(screen.getAllByRole("button", { name: "Take over" }).at(-1)!);

    await waitFor(() => {
      expect(screen.getByText("No recorded work scene.")).toBeInTheDocument();
    });
  });
});

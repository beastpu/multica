import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const runtimesPage = vi.fn<(props: Record<string, unknown>) => null>(() => null);

vi.mock("@multica/views/runtimes", () => ({
  RuntimesPage: (props: Record<string, unknown>) => runtimesPage(props),
}));

import Page from "./page";

describe("web runtimes page", () => {
  it("enables the server-authorized Cloud Runtime access check without a build flag", () => {
    render(<Page />);

    expect(runtimesPage).toHaveBeenCalledWith({ cloudRuntimeEnabled: true });
  });
});

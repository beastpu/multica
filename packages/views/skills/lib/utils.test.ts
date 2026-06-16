import { describe, it, expect } from "vitest";
import { extractSkillUrl, isNameConflictError } from "./utils";

describe("extractSkillUrl", () => {
  it("pulls the URL out of an Atlas install prompt with Chinese prose", () => {
    const pasted =
      "阅读 Atlas 官方安装说明 https://atlas-ai-api.lilithgames.com/api/skill-hub-internal/ocr-review/install-prompt?t=1781513638777，先总结这个 Skill 是干什么的、适合什么场景，安装会改动什么环境。然后询问我是否安装。";
    expect(extractSkillUrl(pasted)).toBe(
      "https://atlas-ai-api.lilithgames.com/api/skill-hub-internal/ocr-review/install-prompt?t=1781513638777",
    );
  });

  it("returns a clean URL unchanged", () => {
    const url = "https://clawhub.ai/owner/skill";
    expect(extractSkillUrl(url)).toBe(url);
  });

  it("trims surrounding whitespace from a clean URL", () => {
    expect(extractSkillUrl("  https://github.com/owner/repo \n")).toBe(
      "https://github.com/owner/repo",
    );
  });

  it("strips trailing sentence punctuation left attached by prose", () => {
    expect(
      extractSkillUrl("Install from https://skills.sh/owner/repo/skill."),
    ).toBe("https://skills.sh/owner/repo/skill");
    expect(
      extractSkillUrl("see https://github.com/owner/repo, then run it"),
    ).toBe("https://github.com/owner/repo");
  });

  it("extracts the first URL when several appear", () => {
    expect(
      extractSkillUrl(
        "https://atlas-ai.lilithgames.com/a then https://clawhub.ai/b",
      ),
    ).toBe("https://atlas-ai.lilithgames.com/a");
  });

  it("finds a URL across multiple lines", () => {
    const pasted = "Skill install\nhttps://github.com/owner/repo\nthanks";
    expect(extractSkillUrl(pasted)).toBe("https://github.com/owner/repo");
  });

  it("preserves query strings and slugs with the full Atlas path", () => {
    const url =
      "https://atlas-ai.lilithgames.com/api/skill-hub/ocr-review?from=share";
    expect(extractSkillUrl(url)).toBe(url);
  });

  it("returns a bare host/slug unchanged when there is no scheme", () => {
    expect(extractSkillUrl("clawhub.ai/owner/skill")).toBe(
      "clawhub.ai/owner/skill",
    );
    expect(extractSkillUrl("  owner/skill  ")).toBe("owner/skill");
  });

  it("returns empty string for blank input", () => {
    expect(extractSkillUrl("   ")).toBe("");
  });
});

describe("isNameConflictError", () => {
  it("matches conflict signals", () => {
    expect(isNameConflictError("409 conflict")).toBe(true);
    expect(isNameConflictError("name already exists")).toBe(true);
    expect(isNameConflictError("unique constraint violated")).toBe(true);
  });

  it("ignores unrelated errors", () => {
    expect(isNameConflictError("network timeout")).toBe(false);
  });
});

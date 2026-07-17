import { describe, expect, it } from "vitest";
import { DOWNLOAD_BASE_URL, parseReleaseAssets } from "./parse-release-assets";

function asset(name: string) {
  return {
    name,
    browser_download_url: `https://github.test/releases/${name}`,
  };
}

describe("parseReleaseAssets", () => {
  it("keeps both Apple Silicon and Intel macOS installers", () => {
    const assets = parseReleaseAssets([
      asset("multica-desktop-0.4.2-mac-arm64.dmg"),
      asset("multica-desktop-0.4.2-mac-arm64.zip"),
      asset("multica-desktop-0.4.2-mac-x64.dmg"),
      asset("multica-desktop-0.4.2-mac-x64.zip"),
      asset("multica-desktop-0.4.2-mac-x64.dmg.blockmap"),
      asset("latest-x64-mac.yml"),
    ]);

    expect(assets).toEqual({
      macArm64Dmg: `${DOWNLOAD_BASE_URL}/multica-desktop-0.4.2-mac-arm64.dmg`,
      macArm64Zip: `${DOWNLOAD_BASE_URL}/multica-desktop-0.4.2-mac-arm64.zip`,
      macX64Dmg: `${DOWNLOAD_BASE_URL}/multica-desktop-0.4.2-mac-x64.dmg`,
      macX64Zip: `${DOWNLOAD_BASE_URL}/multica-desktop-0.4.2-mac-x64.zip`,
    });
  });
});

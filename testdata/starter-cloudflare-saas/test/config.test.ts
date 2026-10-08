import { describe, expect, it } from "vitest";
import wranglerText from "../wrangler.jsonc?raw";
import { findPreviewIsolationProblems, parseJsonc } from "../scripts/previews-guard.mjs";

// D6: previews must bind separate resources and dev login must never be on in config.
describe("wrangler.jsonc (decision D6)", () => {
  const config = parseJsonc(wranglerText);

  it("has a previews block whose bindings do not reuse production resources", () => {
    expect(findPreviewIsolationProblems(config)).toEqual([]);
  });

  it("rebinds D1, KV and R2 under the same binding names", () => {
    const names = (c: typeof config) => ({
      d1: c.d1_databases.map((b: { binding: string }) => b.binding),
      kv: c.kv_namespaces.map((b: { binding: string }) => b.binding),
      r2: c.r2_buckets.map((b: { binding: string }) => b.binding),
    });
    expect(names(config.previews)).toEqual(names(config));
  });

  it("never enables dev login or labels a preview as production", () => {
    expect(config.vars.ALLOW_DEV_LOGIN).toBe("false");
    expect(config.previews.vars.ALLOW_DEV_LOGIN).toBe("false");
    expect(config.previews.vars.ENVIRONMENT).toBe("preview");
    expect(config.vars.ENVIRONMENT).toBe("production");
  });

  it("the guard catches a preview that reuses production ids", () => {
    const broken = structuredClone(config);
    broken.previews.kv_namespaces[0].id = broken.kv_namespaces[0].id;
    broken.previews.d1_databases[0].database_id = broken.d1_databases[0].database_id;
    const problems = findPreviewIsolationProblems(broken);
    expect(problems).toHaveLength(2);
    expect(problems.join("\n")).toMatch(/reuses the production KV namespace id/);
    expect(problems.join("\n")).toMatch(/reuses the production D1 database_id/);
  });

  it("the guard flags a missing previews block or an unrebound binding", () => {
    expect(findPreviewIsolationProblems({ ...config, previews: undefined })[0]).toMatch(/no "previews" block/);
    const missing = structuredClone(config);
    missing.previews.r2_buckets = [];
    expect(findPreviewIsolationProblems(missing).join("\n")).toMatch(/does not rebind "ATTACHMENTS"/);
  });
});

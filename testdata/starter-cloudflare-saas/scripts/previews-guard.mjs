// Pure helpers (no Node imports, so the Vitest suite can import them inside workerd).
// They enforce decision D6: preview bindings must never point at production resources.

/**
 * Parses JSON with comments (what wrangler.jsonc is). Handles // and /* *\/
 * comments and trailing commas, and leaves string contents untouched.
 * @param {string} text
 * @returns {any}
 */
export function parseJsonc(text) {
  let out = "";
  let inString = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    const next = text[i + 1];
    if (inString) {
      out += ch;
      if (ch === "\\") out += text[++i] ?? "";
      else if (ch === '"') inString = false;
    } else if (ch === '"') {
      inString = true;
      out += ch;
    } else if (ch === "/" && next === "/") {
      while (i < text.length && text[i] !== "\n") i++;
      out += "\n";
    } else if (ch === "/" && next === "*") {
      i += 2;
      while (i < text.length && !(text[i] === "*" && text[i + 1] === "/")) i++;
      i++;
    } else {
      out += ch;
    }
  }
  return JSON.parse(out.replace(/,(\s*[}\]])/g, "$1"));
}

/**
 * Returns a list of human-readable problems; an empty list means the previews
 * block is present and shares no resource with the top-level (production) config.
 * @param {any} config parsed wrangler config
 * @returns {string[]}
 */
export function findPreviewIsolationProblems(config) {
  /** @type {string[]} */
  const problems = [];
  const previews = config?.previews;
  if (!previews || typeof previews !== "object") {
    return ['wrangler config has no "previews" block (required by `wrangler preview`)'];
  }

  /** @type {Array<[string, string, string]>} [config key, id field, label] */
  const kinds = [
    ["d1_databases", "database_id", "D1 database_id"],
    ["d1_databases", "database_name", "D1 database_name"],
    ["kv_namespaces", "id", "KV namespace id"],
    ["r2_buckets", "bucket_name", "R2 bucket_name"],
  ];
  for (const [key, field, label] of kinds) {
    const prod = new Set((config[key] ?? []).map((/** @type {any} */ b) => b[field]));
    for (const binding of previews[key] ?? []) {
      if (prod.has(binding[field])) {
        problems.push(`previews.${key} binding "${binding.binding}" reuses the production ${label} "${binding[field]}"`);
      }
    }
  }
  for (const key of ["d1_databases", "kv_namespaces", "r2_buckets"]) {
    const prodNames = (config[key] ?? []).map((/** @type {any} */ b) => b.binding);
    const previewNames = new Set((previews[key] ?? []).map((/** @type {any} */ b) => b.binding));
    for (const name of prodNames) {
      if (!previewNames.has(name)) {
        problems.push(`previews.${key} does not rebind "${name}", so a preview would fall back to a production resource`);
      }
    }
  }
  if (previews.vars?.ALLOW_DEV_LOGIN === "true" || config.vars?.ALLOW_DEV_LOGIN === "true") {
    problems.push('ALLOW_DEV_LOGIN must not be "true" in wrangler.jsonc (use .dev.vars locally)');
  }
  for (const key of ["services", "routes", "triggers", "queues", "workflows"]) {
    if (previews[key] === undefined && config[key] !== undefined) {
      problems.push(
        `top-level "${key}" is not isolated by previews and would target production: remove it or override it in previews`,
      );
    }
  }
  return problems;
}

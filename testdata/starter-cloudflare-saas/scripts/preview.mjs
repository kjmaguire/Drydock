// The ONLY deployment-shaped command agents may run (decision D6).
//
//   npm run preview -- [--name <preview-name>] [--tag <t>] [--message <m>] [--dry-run]
//
// It builds, checks that the `previews` block is isolated from production, then
// runs exactly `wrangler preview ...`. It never runs `wrangler deploy`, and it
// accepts only a fixed allowlist of options, so no other subcommand can be smuggled in.
//
// `--versions` switches to `wrangler versions upload`. That command's Version
// URLs use the Worker's PRODUCTION bindings, so it additionally requires
// ALLOW_VERSIONS_UPLOAD=1.
//
// `--dry-run` prints the command and exits without building or contacting Cloudflare.
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { findPreviewIsolationProblems, parseJsonc } from "./previews-guard.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const wranglerBin = join(root, "node_modules", "wrangler", "bin", "wrangler.js");
const reactRouterBin = join(root, "node_modules", "@react-router", "dev", "bin.js");

const argv = process.argv.slice(2);
const dryRun = argv.includes("--dry-run");
const useVersions = argv.includes("--versions");
const passthrough = argv.filter((arg) => arg !== "--dry-run" && arg !== "--versions");

// Strict allowlist of `wrangler preview` options; anything else is refused.
const VALUE_FLAGS = new Set(["--name", "--tag", "--message", "--worker-name"]);
const BOOLEAN_FLAGS = new Set(["--json", "--ignore-base-config"]);
for (let i = 0; i < passthrough.length; i++) {
  const arg = /** @type {string} */ (passthrough[i]);
  if (BOOLEAN_FLAGS.has(arg)) continue;
  if (VALUE_FLAGS.has(arg) && i + 1 < passthrough.length) {
    i++;
    continue;
  }
  console.error(
    `error: refusing argument "${arg}": this script only runs \`wrangler preview\` with --name, --tag, --message, --worker-name, --json, --ignore-base-config`,
  );
  process.exit(2);
}

const config = parseJsonc(readFileSync(join(root, "wrangler.jsonc"), "utf8"));
const problems = findPreviewIsolationProblems(config);
if (problems.length > 0) {
  for (const problem of problems) console.error(`error: ${problem}`);
  console.error("error: previews are not isolated from production (D6); fix wrangler.jsonc first");
  process.exit(3);
}

if (useVersions && process.env.ALLOW_VERSIONS_UPLOAD !== "1") {
  console.error(
    "error: `versions upload` Version URLs use PRODUCTION bindings. Prefer `wrangler preview`; set ALLOW_VERSIONS_UPLOAD=1 to override.",
  );
  process.exit(3);
}

const wranglerArgs = useVersions ? ["versions", "upload", ...passthrough] : ["preview", ...passthrough];
console.log(`wrangler ${wranglerArgs.join(" ")}`);
if (dryRun) {
  console.log("dry run: nothing was built or uploaded");
  process.exit(0);
}

const env = { ...process.env, WRANGLER_SEND_METRICS: "false" };
const build = spawnSync(process.execPath, [reactRouterBin, "build"], { cwd: root, stdio: "inherit", env });
if (build.status !== 0) process.exit(build.status ?? 1);
const result = spawnSync(process.execPath, [wranglerBin, ...wranglerArgs], { cwd: root, stdio: "inherit", env });
process.exit(result.status ?? 1);

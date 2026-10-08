// Local dev server: `wrangler dev` on ports taken from the environment so that
// several git worktrees can run side by side.
//
//   PORT=8801 INSPECTOR_PORT=9301 npm run dev
//
// PORT / INSPECTOR_PORT unset => a free port is allocated by the OS.
// WRANGLER_STATE_DIR (default .wrangler/state) selects the local D1/KV/R2 state;
// it is per directory, so each worktree gets its own database automatically.
// RESET_STATE=1 wipes the state dir first (used by the e2e web server for a clean DB).
// Flags: --build  always rebuild first (default: only when no build exists).
import { spawn, spawnSync } from "node:child_process";
import { copyFileSync, existsSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const wranglerBin = join(root, "node_modules", "wrangler", "bin", "wrangler.js");
const reactRouterBin = join(root, "node_modules", "@react-router", "dev", "bin.js");
const stateDir = process.env.WRANGLER_STATE_DIR || ".wrangler/state";

function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = /** @type {import("node:net").AddressInfo} */ (server.address());
      server.close(() => resolve(port));
    });
  });
}

/** @param {string | undefined} raw @param {string} name */
async function resolvePort(raw, name) {
  if (raw === undefined || raw === "") return freePort();
  const port = Number(raw);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    console.error(`error: ${name}=${raw} is not a valid port`);
    process.exit(2);
  }
  return port;
}

/** @param {string} bin @param {string[]} args */
function runSync(bin, args) {
  const result = spawnSync(process.execPath, [bin, ...args], {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, WRANGLER_SEND_METRICS: "false" },
  });
  if (result.status !== 0) process.exit(result.status ?? 1);
}

const port = await resolvePort(process.env.PORT, "PORT");
const inspectorPort = await resolvePort(process.env.INSPECTOR_PORT, "INSPECTOR_PORT");
if (port === inspectorPort) {
  console.error("error: PORT and INSPECTOR_PORT must differ");
  process.exit(2);
}

if (!existsSync(join(root, ".dev.vars"))) {
  copyFileSync(join(root, ".dev.vars.example"), join(root, ".dev.vars"));
  console.log("created .dev.vars from .dev.vars.example (local-only values, gitignored)");
}
if (process.argv.includes("--build") || !existsSync(join(root, "build", "server", "wrangler.json"))) {
  runSync(reactRouterBin, ["build"]);
}
if (process.env.RESET_STATE === "1") rmSync(join(root, stateDir), { recursive: true, force: true });
runSync(wranglerBin, ["d1", "migrations", "apply", "DB", "--local", "--persist-to", stateDir]);

console.log(`DRYDOCK_DEV_URL=http://localhost:${port}`);
console.log(`DRYDOCK_DEV_PORT=${port} DRYDOCK_INSPECTOR_PORT=${inspectorPort} DRYDOCK_STATE_DIR=${stateDir}`);

const child = spawn(
  process.execPath,
  [
    wranglerBin,
    "dev",
    "--ip",
    "127.0.0.1",
    "--port",
    String(port),
    "--inspector-port",
    String(inspectorPort),
    "--persist-to",
    stateDir,
  ],
  { cwd: root, stdio: "inherit", env: { ...process.env, WRANGLER_SEND_METRICS: "false" } },
);
for (const signal of ["SIGINT", "SIGTERM", "SIGBREAK"]) {
  process.on(signal, () => child.kill());
}
child.on("exit", (code) => process.exit(code ?? 0));

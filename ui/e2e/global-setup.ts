// Builds cmd/drydock-e2e (the Go test harness: real core, scripted fake agent) and starts it. The UI under test is the
// built ui/dist, served by the Go process itself. The harness prints the URL (with the token) and fresh git repositories.
//
// Two harness processes are started from the same binary:
//   1. the default harness: a build with NO license keys (licensing "not configured", everything allowed);
//   2. `-licensed`: a TEST HARNESS MODE with a throwaway license key, which starts with no license (read-only) and prints
//      two signed test tokens (one active, one expired). It is only used by e2e/round4.spec.ts.
import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { copyFileSync, existsSync, linkSync, mkdirSync, readdirSync, rmSync, statSync, symlinkSync } from 'node:fs';
import { delimiter, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const uiDir = resolve(here, '..');
const repoRoot = resolve(uiDir, '..');
const REPOS = 48;
const GREEN = 8;
const LICENSED_REPOS = 4;

interface Started {
  child: ChildProcess;
  url: string;
  repos: string[];
  greens: string[];
  docs: string;
  extra: Record<string, string>;
}

/**
 * The PATH the harness runs with: the same, except that the GitHub CLI is not on it. The harness has no GitHub account, so
 * the suite is the same on a developer machine that has gh signed in as on CI with it installed (the wizard's GitHub choice
 * then explains that gh is missing). A folder that holds gh is replaced by a folder of links to everything else in it; on
 * Windows a folder of its own (C:\Program Files\GitHub CLI: no other program in it) is simply left out.
 */
function pathWithoutGh(outDir: string): string {
  const win = process.platform === 'win32';
  const ghName = win ? 'gh.exe' : 'gh';
  const dirs = (process.env.PATH ?? '').split(delimiter);
  const shimRoot = join(outDir, 'path-without-gh');
  rmSync(shimRoot, { recursive: true, force: true });
  return dirs
    .map((d, i) => {
      if (!d || !existsSync(join(d, ghName))) return d;
      const others = readdirSync(d).filter((f) => f.toLowerCase() !== ghName);
      if (win && !others.some((f) => /\.(exe|cmd|bat|com|ps1)$/i.test(f))) return '';
      const shim = join(shimRoot, String(i));
      mkdirSync(shim, { recursive: true });
      for (const f of others) linkInto(join(d, f), join(shim, f));
      return shim;
    })
    .filter((d) => d !== '')
    .join(delimiter);
}

/** A link at to that reaches from: a symlink, else (Windows without the right to make one) a hard link or a copy. */
function linkInto(from: string, to: string): void {
  const dir = statSync(from, { throwIfNoEntry: false })?.isDirectory() === true;
  try {
    symlinkSync(from, to, dir ? 'junction' : 'file');
  } catch (err) {
    if (process.platform !== 'win32' || dir) throw err;
    try {
      linkSync(from, to);
    } catch {
      copyFileSync(from, to);
    }
  }
}

let harnessPath: string | undefined;

async function start(exe: string, args: string[]): Promise<Started> {
  const env = harnessPath ? { ...process.env, PATH: harnessPath } : process.env;
  const child = spawn(exe, ['-ui', join(uiDir, 'dist'), ...args], { cwd: repoRoot, env, stdio: ['ignore', 'pipe', 'pipe'] });
  const out: Started = { child, url: '', repos: [], greens: [], docs: '', extra: {} };
  let stderr = '';
  child.stderr!.on('data', (d) => (stderr += String(d)));
  await new Promise<void>((resolveReady, reject) => {
    const timer = setTimeout(() => reject(new Error(`drydock-e2e did not start in 60s\n${stderr}`)), 60_000);
    let buf = '';
    child.stdout!.on('data', (d) => {
      buf += String(d);
      let i: number;
      while ((i = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, i).trim();
        buf = buf.slice(i + 1);
        if (line.startsWith('DRYDOCK_E2E_REPO ')) out.repos.push(line.slice('DRYDOCK_E2E_REPO '.length));
        else if (line.startsWith('DRYDOCK_E2E_GREEN_REPO ')) out.greens.push(line.slice('DRYDOCK_E2E_GREEN_REPO '.length));
        else if (line.startsWith('DRYDOCK_E2E_DOCS ')) out.docs = line.slice('DRYDOCK_E2E_DOCS '.length);
        else if (line.startsWith('DRYDOCK_E2E_OAUTH_MCP ')) out.extra.oauth = line.slice('DRYDOCK_E2E_OAUTH_MCP '.length);
        else if (line.startsWith('DRYDOCK_E2E_WSL ')) out.extra.wsl = line.slice('DRYDOCK_E2E_WSL '.length);
        else if (line.startsWith('DRYDOCK_E2E_LINEAR_API ')) out.extra.linear = line.slice('DRYDOCK_E2E_LINEAR_API '.length);
        else if (line.startsWith('DRYDOCK_E2E_LICENSE_ACTIVE ')) out.extra.active = line.slice('DRYDOCK_E2E_LICENSE_ACTIVE '.length);
        else if (line.startsWith('DRYDOCK_E2E_LICENSE_EXPIRED ')) out.extra.expired = line.slice('DRYDOCK_E2E_LICENSE_EXPIRED '.length);
        else if (line.startsWith('DRYDOCK_E2E ')) {
          out.url = line.slice('DRYDOCK_E2E '.length);
          clearTimeout(timer);
          resolveReady();
        }
      }
    });
    child.on('exit', (code) => {
      clearTimeout(timer);
      if (!out.url) reject(new Error(`drydock-e2e exited with ${code} before it was ready\n${stderr}`));
    });
  });
  return out;
}

export default async function globalSetup() {
  if (!existsSync(join(uiDir, 'dist', 'index.html'))) {
    throw new Error('ui/dist is missing: run `npm run build` first (task ui:build)');
  }
  const outDir = join(uiDir, '.e2e');
  mkdirSync(outDir, { recursive: true });
  const exe = join(outDir, process.platform === 'win32' ? 'drydock-e2e.exe' : 'drydock-e2e');
  const build = spawnSync('go', ['build', '-o', exe, './cmd/drydock-e2e'], {
    cwd: repoRoot,
    env: { ...process.env, GOFLAGS: process.env.GOFLAGS ?? '-mod=mod' },
    encoding: 'utf8',
  });
  if (build.status !== 0) throw new Error(`go build ./cmd/drydock-e2e failed:\n${build.stdout}\n${build.stderr}`);

  harnessPath = pathWithoutGh(outDir);
  // which fresh repositories the tests have taken (e2e/helpers.ts claim): empty for every run
  const claims = join(outDir, 'claims');
  rmSync(claims, { recursive: true, force: true });
  mkdirSync(claims, { recursive: true });
  process.env.DRYDOCK_E2E_CLAIMS = claims;
  process.env.DRYDOCK_E2E_EXE = exe; // the binary is also a stdio MCP server when DRYDOCK_E2E_MCP_SERVER=1 (a test fixture)
  const main = await start(exe, ['-repos', String(REPOS), '-green-repos', String(GREEN)]);
  const started: ChildProcess[] = [main.child];
  const stopAll = async () => {
    for (const c of started) c.kill('SIGTERM');
    await new Promise((r) => setTimeout(r, 500));
    for (const c of started) if (c.exitCode === null) c.kill('SIGKILL');
  };
  process.env.DRYDOCK_E2E_URL = main.url;
  process.env.DRYDOCK_E2E_REPOS = JSON.stringify(main.repos);
  process.env.DRYDOCK_E2E_GREEN_REPOS = JSON.stringify(main.greens);
  process.env.DRYDOCK_E2E_DOCS = main.docs;
  process.env.DRYDOCK_E2E_OAUTH_MCP = main.extra.oauth ?? '';
  process.env.DRYDOCK_E2E_WSL = main.extra.wsl ?? ''; // the stand-in WSL distro (a test double; Linux hosts only)
  process.env.DRYDOCK_E2E_LINEAR_API = main.extra.linear ?? ''; // the Linear protocol fixture (a test double)

  // TEST HARNESS MODE: a build that has a (throwaway) license key. See cmd/drydock-e2e/main.go.
  let licensed: Awaited<ReturnType<typeof start>>;
  try {
    licensed = await start(exe, ['-licensed', '-repos', String(LICENSED_REPOS)]);
  } catch (err) {
    await stopAll(); // do not leak the first harness (its port and temp dir) when the second one fails to start
    throw err;
  }
  started.push(licensed.child);
  process.env.DRYDOCK_E2E_LICENSED_URL = licensed.url;
  process.env.DRYDOCK_E2E_LICENSED_REPOS = JSON.stringify(licensed.repos);
  process.env.DRYDOCK_E2E_LICENSE_ACTIVE = licensed.extra.active ?? '';
  process.env.DRYDOCK_E2E_LICENSE_EXPIRED = licensed.extra.expired ?? '';

  return stopAll;
}

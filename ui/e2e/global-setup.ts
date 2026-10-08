// Builds cmd/drydock-e2e (the Go test harness: real core, scripted fake agent) and starts it. The UI under test is the
// built ui/dist, served by the Go process itself. The harness prints the URL (with the token) and fresh git repositories.
//
// Two harness processes are started from the same binary:
//   1. the default harness: a build with NO license keys (licensing "not configured", everything allowed);
//   2. `-licensed`: a TEST HARNESS MODE with a throwaway license key, which starts with no license (read-only) and prints
//      two signed test tokens (one active, one expired). It is only used by e2e/round4.spec.ts.
import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { mkdirSync, existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const uiDir = resolve(here, '..');
const repoRoot = resolve(uiDir, '..');
const REPOS = 20;
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

async function start(exe: string, args: string[]): Promise<Started> {
  const child = spawn(exe, ['-ui', join(uiDir, 'dist'), ...args], { cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe'] });
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

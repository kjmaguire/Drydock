// Builds cmd/drydock-e2e (the Go test harness: real core, scripted fake agent) and starts it. The UI under test is the
// built ui/dist, served by the Go process itself. The harness prints the URL (with the token) and fresh git repositories.
import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { mkdirSync, existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const uiDir = resolve(here, '..');
const repoRoot = resolve(uiDir, '..');
const REPOS = 12;

let child: ChildProcess | undefined;

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

  child = spawn(exe, ['-ui', join(uiDir, 'dist'), '-repos', String(REPOS)], { cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe'] });
  const repos: string[] = [];
  let url = '';
  let stderr = '';
  child.stderr!.on('data', (d) => (stderr += String(d)));
  await new Promise<void>((resolveReady, reject) => {
    const timer = setTimeout(() => reject(new Error(`drydock-e2e did not start in 60s\n${stderr}`)), 60_000);
    let buf = '';
    child!.stdout!.on('data', (d) => {
      buf += String(d);
      let i: number;
      while ((i = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, i).trim();
        buf = buf.slice(i + 1);
        if (line.startsWith('DRYDOCK_E2E_REPO ')) repos.push(line.slice('DRYDOCK_E2E_REPO '.length));
        else if (line.startsWith('DRYDOCK_E2E ')) {
          url = line.slice('DRYDOCK_E2E '.length);
          clearTimeout(timer);
          resolveReady();
        }
      }
    });
    child!.on('exit', (code) => {
      clearTimeout(timer);
      if (!url) reject(new Error(`drydock-e2e exited with ${code} before it was ready\n${stderr}`));
    });
  });
  process.env.DRYDOCK_E2E_URL = url;
  process.env.DRYDOCK_E2E_REPOS = JSON.stringify(repos);

  return async () => {
    child?.kill('SIGTERM');
    await new Promise((r) => setTimeout(r, 500));
    if (child && child.exitCode === null) child.kill('SIGKILL');
  };
}

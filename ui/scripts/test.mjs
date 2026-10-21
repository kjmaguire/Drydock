// Runs `remix test` (unit tests in Node, component tests in a real Chromium page through Playwright).
//
// remix test launches Playwright's own Chromium and does not take an executable path. Where the browsers are preinstalled
// for an older Playwright (a sandbox with PLAYWRIGHT_BROWSERS_PATH set and `playwright install` not allowed), this script
// builds a directory of links that presents the installed Chromium under the revision this Playwright expects, and points
// PLAYWRIGHT_BROWSERS_PATH at it. Where the expected revision is installed (CI after `npx playwright install chromium`)
// nothing changes.
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, symlinkSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';

const require = createRequire(import.meta.url);

function expectedRevision() {
  const pkg = require.resolve('playwright-core/package.json');
  const browsers = JSON.parse(readFileSync(join(dirname(pkg), 'browsers.json'), 'utf8')).browsers;
  return browsers.find((b) => b.name === 'chromium-headless-shell')?.revision;
}

function shim(base, rev) {
  if (existsSync(join(base, `chromium_headless_shell-${rev}`))) return undefined;
  const have = readdirSync(base)
    .filter((d) => /^chromium_headless_shell-\d+$/.test(d))
    .sort((a, b) => Number(b.split('-')[1]) - Number(a.split('-')[1]))[0];
  if (!have) return undefined;
  const src = join(base, have, 'chrome-linux');
  if (!existsSync(join(src, 'headless_shell'))) return undefined;
  const out = join(tmpdir(), `drydock-pw-${rev}-from-${have.split('-')[1]}`);
  rmSync(out, { recursive: true, force: true });
  const dir = join(out, `chromium_headless_shell-${rev}`, 'chrome-headless-shell-linux64');
  mkdirSync(dir, { recursive: true });
  for (const f of readdirSync(src)) symlinkSync(join(src, f), join(dir, f));
  symlinkSync(join(src, 'headless_shell'), join(dir, 'chrome-headless-shell'));
  return out;
}

const env = { ...process.env };
const base = env.PLAYWRIGHT_BROWSERS_PATH;
const rev = expectedRevision();
if (base && rev && existsSync(base)) {
  const out = shim(base, rev);
  if (out) {
    env.PLAYWRIGHT_BROWSERS_PATH = out;
    console.log(`test: using the preinstalled Chromium in ${base} as revision ${rev}`);
  }
}

const bin = join(dirname(require.resolve('remix/package.json')), 'dist', 'cli-entry.js');
const r = spawnSync(process.execPath, [bin, 'test', ...process.argv.slice(2)], { stdio: 'inherit', env });
process.exit(r.status ?? 1);

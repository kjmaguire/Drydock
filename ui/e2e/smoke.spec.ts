// The smoke test of the whole app (the review of 2026-10-26): every page of the real app on the real core, with a project, two
// agents, a task waiting for approval, its run and a chat, opened one after another as a person would. On each page:
//   - no uncaught exception, no console error and no failed request;
//   - no call to the core answered with a server error (5xx), and no refusal (4xx) the page did not expect (EXPECTED_REFUSALS);
//   - the page shows its heading within the budget (PAGE_BUDGET_MS) and settles without calling one method over and over.
// What every page cost (time to its heading, its calls to the core) is written to test-results/smoke-report.json.
import { writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Page } from '@playwright/test';
import { appUrl, choose, createAgent, createProject, openNewAgent, projectNav } from './helpers';

const ROUTES = [
  '/', '/needs-you', '/rooms', '/tasks', '/tasks/new', '/pulls', '/team', '/crew', '/activity', '/project', '/project/integrations', '/plan', '/requirements',
  '/milestones', '/memory', '/skills', '/packs', '/integrations', '/integrations/linear', '/integrations/mslearn', '/integrations/openrouter', '/budgets', '/evals', '/security', '/repo-map', '/knowledge', '/home', '/chats',
  '/library', '/library/skills', '/connections', '/agents', '/agents/claude', '/github', '/security-scan', '/models', '/models/new', '/routing', '/backups', '/data', '/support', '/license',
  '/settings', '/guide', '/guide/scan',
];

/** Refusals a page asks for on purpose (a method answering "not here" is how the page learns it), with the reason. */
const EXPECTED_REFUSALS: { method: RegExp; status: number; why: string }[] = [];

const PAGE_BUDGET_MS = 3000;
/** One call to the core answered slower than this is slow for a desktop app on the same machine. */
const CALL_BUDGET_MS = 1000;
/**
 * Calls whose work is outside the core's own data, each with its own budget and the reason. A page shows a loading state for
 * them; their answers are kept for a while.
 */
const SLOW_CALLS: Record<string, { ms: number; why: string }> = {
  'connection.list': {
    ms: 3000,
    why: 'starts every agent CLI for its version and its sign-in (side by side since the review; 0.6 to 1.2 s with the stand-ins, depending on the load of the machine)',
  },
  'github.projectRepos': {
    ms: 5000,
    why: "reads every project's origin remote with git (eight at a time; on Windows a git start is slow, 1.4 s for 60 projects) and asks GitHub about each repository; the table shows a loading row and keeps the answers 60 s",
  },
};
/** One method called more often than this while one page opens and settles is a page asking again and again. */
const REPEAT_LIMIT = 6;

interface Call {
  method: string;
  status: number;
  ms: number;
  error?: string;
  /** What a refused call asked for (its parameters), to find which page or panel asked. */
  params?: string;
}

interface PageReport {
  path: string;
  headingMs: number;
  calls: Call[];
  problems: string[];
}

function watch(page: Page) {
  const state = { calls: [] as Call[], problems: [] as string[] };
  page.on('pageerror', (err) => state.problems.push(`uncaught: ${err.message}`));
  page.on('console', (msg) => {
    if (msg.type() === 'error') state.problems.push(`console error: ${msg.text().slice(0, 300)}`);
  });
  page.on('requestfailed', (req) => {
    const f = req.failure()?.errorText ?? '';
    // a page left while its event stream or a call was open aborts it: that is the navigation, not a failure
    if (/ERR_ABORTED/.test(f)) return;
    state.problems.push(`request failed: ${req.method()} ${new URL(req.url()).pathname} ${f}`);
  });
  const started = new Map<unknown, number>();
  page.on('request', (req) => started.set(req, Date.now()));
  page.on('response', async (resp) => {
    const u = new URL(resp.url());
    if (!u.pathname.startsWith('/rpc/')) {
      if (resp.status() >= 400) state.problems.push(`HTTP ${resp.status()} for ${u.pathname}`);
      return;
    }
    const method = decodeURIComponent(u.pathname.slice('/rpc/'.length));
    const t0 = started.get(resp.request());
    started.delete(resp.request());
    const call: Call = { method, status: resp.status(), ms: t0 === undefined ? -1 : Date.now() - t0 };
    if (resp.status() >= 400) {
      try {
        const body = (await resp.json()) as { error?: string };
        call.error = (body.error ?? '').slice(0, 200);
      } catch {
        call.error = '(no JSON body)';
      }
      call.params = (resp.request().postData() ?? '').slice(0, 200);
    }
    state.calls.push(call);
  });
  return state;
}

test('smoke: every page opens on the real core without an error, a failed call or a page that keeps asking, within its time budget', async ({ page }) => {
  test.setTimeout(900_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  await createProject(page, 'gocalc-smoke');
  await createAgent(page, 'Worker One', 'backend');
  await openNewAgent(page);
  await page.getByLabel('Agent name').fill('Reviewer One');
  await page.getByLabel('Role', { exact: true }).fill('reviewer');
  await choose(page.getByLabel('Adapter', { exact: true }), 'claude');
  await page.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Reviewer One' })).toBeVisible();
  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill('Fix Subtract');
  await page.getByLabel('Description').fill('Subtract returns 0; make it return a - b.');
  await page.getByLabel('Acceptance criteria').fill('Given 5 and 3 When Subtract is called Then 2');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Fix Subtract/ })).toBeVisible();
  const taskUrl = new URL(page.url());
  await choose(page.getByLabel('Worker', { exact: true }), { label: 'Worker One (backend)' });
  await choose(page.getByLabel('Reviewer', { exact: true }), { label: 'Reviewer One (reviewer)' });
  await page.getByRole('button', { name: 'Assign' }).click();
  await page.getByRole('button', { name: 'Start', exact: true }).click();
  await expect(page.getByText('Needs your decision')).toBeVisible({ timeout: 120_000 });

  const state = watch(page);
  const base = new URL(appUrl());
  const pages = [...ROUTES, taskUrl.pathname, `${taskUrl.pathname}/run`];
  const reports: PageReport[] = [];
  for (const path of pages) {
    state.calls = [];
    state.problems = [];
    const u = new URL(base.toString());
    u.pathname = path;
    const t0 = Date.now();
    await page.goto(u.toString());
    await expect(page.locator('#main h1, #main [role=heading]').first()).toBeVisible();
    const headingMs = Date.now() - t0;
    // settle: what the page asks for once it is drawn
    await page.waitForTimeout(1500);
    const r: PageReport = { path, headingMs, calls: [...state.calls], problems: [...state.problems] };
    for (const c of r.calls) {
      if (c.status >= 500) r.problems.push(`${c.method}: HTTP ${c.status} ${c.error ?? ''}`);
      else if (c.status >= 400 && !EXPECTED_REFUSALS.some((x) => x.method.test(c.method) && x.status === c.status)) r.problems.push(`${c.method}: refused ${c.status} ${c.error ?? ''} (asked ${c.params ?? ''})`);
    }
    const counts = new Map<string, number>();
    for (const c of r.calls) counts.set(c.method, (counts.get(c.method) ?? 0) + 1);
    for (const [m, n] of counts) if (n > REPEAT_LIMIT) r.problems.push(`${m} called ${n} times while the page opened`);
    if (headingMs > PAGE_BUDGET_MS) r.problems.push(`heading after ${headingMs} ms (budget ${PAGE_BUDGET_MS} ms)`);
    reports.push(r);
  }
  const dir = join(process.cwd(), 'test-results');
  mkdirSync(dir, { recursive: true });
  let leak: unknown = undefined;
  const problems = reports.flatMap((r) => r.problems.map((p) => `${r.path}: ${p}`));
  for (const c of reports.flatMap((r) => r.calls)) {
    const budget = SLOW_CALLS[c.method]?.ms ?? CALL_BUDGET_MS;
    if (c.ms > budget) problems.push(`${c.method} answered in ${c.ms} ms (budget ${budget} ms)`);
  }

  // moving around without reloading (how the app is used): the sidebar's pages twice over, then the heap, the DOM and the
  // listeners against what they were after the first round; a page that leaves a subscription or a timer behind grows them
  // (a workspace page shows the projects as lines: the round opens the project first; the Agents library's pills and the
  // Settings sections follow their hub's link, decision P140)
  const nav = ['nav-project-item', 'nav-overview', 'nav-rooms', 'nav-overview', 'nav-tasks', 'nav-team', 'nav-project-settings', 'nav-knowledge', 'nav-needs-you', 'nav-chats', 'nav-library', 'nav-library-starters', 'nav-library-skills', 'nav-crew', 'nav-settings', 'nav-side-agents', 'nav-side-integrations', 'nav-side-models', 'nav-security-scan'];
  await page.goto(appUrl());
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Performance.enable');
  const metrics = async () => {
    await cdp.send('HeapProfiler.collectGarbage');
    const { metrics: m } = await cdp.send('Performance.getMetrics');
    const get = (n: string) => m.find((x) => x.name === n)?.value ?? 0;
    return { heap: get('JSHeapUsedSize'), nodes: get('Nodes'), listeners: get('JSEventListeners') };
  };
  const round = async () => {
    for (const id of nav) {
      await page.getByTestId(id).first().click();
      await expect(page.locator('#main h1, #main [role=heading]').first()).toBeVisible();
      await page.waitForTimeout(150);
    }
  };
  await round();
  const after1 = await metrics();
  for (let i = 0; i < 4; i++) await round();
  const after5 = await metrics();
  leak = { after1, after5 };
  if (after5.heap > after1.heap * 1.5 + 2_000_000) problems.push(`the heap grew from ${Math.round(after1.heap / 1e6)} MB to ${Math.round(after5.heap / 1e6)} MB over four more rounds of the sidebar`);
  if (after5.listeners > after1.listeners * 1.5 + 200) problems.push(`event listeners grew from ${after1.listeners} to ${after5.listeners} over four more rounds`);
  if (after5.nodes > after1.nodes * 1.5 + 2000) problems.push(`DOM nodes grew from ${after1.nodes} to ${after5.nodes} over four more rounds`);
  writeFileSync(join(dir, 'smoke-report.json'), JSON.stringify({ pages: reports, leak }, null, 2));
  expect(problems, `${problems.length} problems:\n${problems.join('\n')}`).toEqual([]);
});

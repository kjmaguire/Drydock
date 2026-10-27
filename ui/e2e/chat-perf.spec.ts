// Chat performance on the real core (the review of 2026-10-27). The agent CLI is the harness's scripted stand-in (a TEST
// DOUBLE, cmd/drydock-e2e): "drydock-e2e perf history" makes 300 tool calls and answers with code, "drydock-e2e perf stream"
// streams a long Markdown answer (about 3,000 words, headings, lists, code and tables) one word every 2 ms.
//
// What is measured, and written to test-results/chat-perf-report.json:
//   - the core: the events one streamed answer publishes (each is a row in the events table and a message on the stream);
//   - the page while the answer streams, with a long history open: main-thread time (CDP Performance metrics), long tasks
//     (over 50 ms), how long a key typed in the composer takes to show (Event Timing), the frames drawn;
//   - opening the chat with that history: time to its newest message.
// The budgets below are what a desktop app must hold on an ordinary PC; the test fails when one is missed.
import { writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Page } from '@playwright/test';
import { createProject, workspaceNav } from './helpers';

/** Main-thread busy time while the answer streams, as a share of the streaming time. */
const STREAM_BUSY_SHARE = 0.35;
/** The longest single task while streaming. */
const LONG_TASK_MS = 200;
/** A key typed in the composer while the answer streams shows within this. */
const KEY_MS = 100;
/** Opening the chat with its long history: its newest message shows within this. */
const OPEN_MS = 2500;

async function rpc<T>(page: Page, method: string, params: unknown = {}): Promise<T> {
  return page.evaluate(
    async ({ method, params }) => {
      const token = sessionStorage.getItem('drydock.token') ?? '';
      const r = await fetch(`/rpc/${method}`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Drydock-Token': token }, body: JSON.stringify(params) });
      return ((await r.json()) as { result: unknown }).result;
    },
    { method, params },
  ) as Promise<T>;
}

async function cdpMetrics(page: Page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Performance.enable');
  // DRYDOCK_PERF_PROFILE=1: a CPU profile of the stream, its top functions by self time printed (build the UI with
  // `npx vite build --minify false` first so they have their names)
  const profile = !!process.env.DRYDOCK_PERF_PROFILE;
  if (profile) {
    await cdp.send('Profiler.enable');
    await cdp.send('Profiler.setSamplingInterval', { interval: 200 });
  }
  let started = false;
  return async () => {
    if (profile && !started) {
      started = true;
      await cdp.send('Profiler.start');
    } else if (profile) {
      const { profile: p } = await cdp.send('Profiler.stop');
      const self = new Map<string, number>();
      const dt = new Map<number, number>();
      p.samples?.forEach((id, i) => dt.set(id, (dt.get(id) ?? 0) + (p.timeDeltas?.[i] ?? 0)));
      for (const n of p.nodes) {
        const f = n.callFrame;
        const k = `${f.functionName || '(anon)'} ${f.url.split('/').pop()}:${f.lineNumber + 1}`;
        self.set(k, (self.get(k) ?? 0) + (dt.get(n.id) ?? 0));
      }
      const top = [...self].sort((a, b) => b[1] - a[1]).slice(0, 40);
      console.log(top.map(([k, us]) => `${(us / 1000).toFixed(0).padStart(6)} ms  ${k}`).join('\n'));
    }
    const { metrics } = await cdp.send('Performance.getMetrics');
    const get = (n: string) => metrics.find((x) => x.name === n)?.value ?? 0;
    return { task: get('TaskDuration'), script: get('ScriptDuration'), layout: get('LayoutDuration'), style: get('RecalcStyleDuration'), nodes: get('Nodes'), heap: get('JSHeapUsedSize') };
  };
}

test('chat performance: a long answer streams over a long history without blocking the page, and the chat opens quickly', async ({ page }) => {
  test.setTimeout(600_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  // what the page receives on its event stream, by type, counted before the app reads it
  await page.addInitScript(() => {
    const w = window as unknown as { __events: Record<string, number>; __eventBytes: number; EventSource: typeof EventSource };
    w.__events = {};
    w.__eventBytes = 0;
    const Orig = w.EventSource;
    w.EventSource = class extends Orig {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init);
        this.addEventListener('drydock', (ev) => {
          const data = (ev as MessageEvent<string>).data;
          w.__eventBytes += data.length;
          const type = /"type":"([^"]+)"/.exec(data)?.[1] ?? '?';
          w.__events[type] = (w.__events[type] ?? 0) + 1;
        });
      }
    } as typeof EventSource;
  });
  await createProject(page, 'chat-perf');
  await workspaceNav(page, 'nav-chats');
  await page.getByTestId('chat-new').click();
  await page.getByRole('dialog', { name: 'New chat' }).getByRole('button', { name: 'Start chat' }).click();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  const chatId = /\/chats\/(chat_[^/?]+)/.exec(page.url())![1]!;
  const message = page.getByLabel('Message', { exact: true });
  const status = page.getByTestId('chat-status');

  // a long history: three turns of 300 tool calls and an answer with code (about 900 rows)
  for (let i = 1; i <= 3; i++) {
    await message.fill(`drydock-e2e perf history ${i}`);
    await message.press('Control+Enter');
    await expect(message).toHaveValue('');
    await expect(status).toHaveText('Session kept', { timeout: 120_000 });
  }
  const all = await rpc<{ messages: unknown[]; hasMore: boolean }>(page, 'chat.messages', { id: chatId, limit: 500 });
  expect(all.messages.length).toBeGreaterThan(300);

  // opening it: a reload, to its newest message
  const t0 = Date.now();
  await page.reload();
  await expect(page.getByTestId('chat-log').getByText('Step 3: what changes').last()).toBeVisible();
  const openMs = Date.now() - t0;

  // the streamed answer, measured
  const metrics = await cdpMetrics(page);
  await page.evaluate(() => {
    const w = window as unknown as { __long: number[]; __keys: number[]; __frames: number; __events: Record<string, number>; __eventBytes: number };
    w.__long = [];
    w.__keys = [];
    w.__frames = 0;
    w.__events = {};
    w.__eventBytes = 0;
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) w.__long.push(e.duration);
    }).observe({ type: 'longtask', buffered: false });
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) if (e.name === 'keydown' || e.name === 'keypress' || e.name === 'input' || e.name === 'keyup') w.__keys.push(e.duration);
    }).observe({ type: 'event', durationThreshold: 16, buffered: false } as PerformanceObserverInit);
    const tick = () => {
      w.__frames++;
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
  const head0 = await rpc<number>(page, 'events.head');
  const before = await metrics();
  await message.fill('drydock-e2e perf stream');
  const s0 = Date.now();
  await message.press('Control+Enter');
  await expect(page.getByTestId('chat-streaming').locator('.md')).toBeVisible({ timeout: 30_000 });
  const firstTextMs = Date.now() - s0;
  // type while it streams, as a person writing the next message would
  await message.click();
  await page.keyboard.type('while it answers, the next question about Subtract', { delay: 40 });
  await expect(status).toHaveText('Session kept', { timeout: 300_000 });
  const streamMs = Date.now() - s0;
  const after = await metrics();
  const head1 = await rpc<number>(page, 'events.head');
  const page1 = await page.evaluate(() => {
    const w = window as unknown as { __long: number[]; __keys: number[]; __frames: number; __events: Record<string, number>; __eventBytes: number };
    return { long: w.__long, keys: w.__keys, frames: w.__frames, events: w.__events, eventBytes: w.__eventBytes };
  });
  await expect(message).toHaveValue('while it answers, the next question about Subtract');

  const busy = after.task - before.task;
  const report = {
    history: { rows: all.messages.length, hasMore: all.hasMore, openMs },
    stream: {
      ms: streamMs,
      firstTextMs,
      eventsPublished: head1 - head0,
      eventsReceived: page1.events,
      eventBytes: page1.eventBytes,
      mainThread: { taskS: busy, scriptS: after.script - before.script, layoutS: after.layout - before.layout, styleS: after.style - before.style, share: busy / (streamMs / 1000) },
      longTasks: { count: page1.long.length, maxMs: Math.max(0, ...page1.long), totalMs: page1.long.reduce((a, b) => a + b, 0) },
      keys: { slow: page1.keys.length, maxMs: Math.max(0, ...page1.keys) },
      fps: page1.frames / (streamMs / 1000),
      nodes: after.nodes,
      heapMB: after.heap / 1e6,
    },
  };
  const dir = join(process.cwd(), 'test-results');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'chat-perf-report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));

  const problems: string[] = [];
  if (openMs > OPEN_MS) problems.push(`the chat opened in ${openMs} ms (budget ${OPEN_MS} ms)`);
  if (report.stream.mainThread.share > STREAM_BUSY_SHARE) problems.push(`the main thread was busy ${Math.round(report.stream.mainThread.share * 100)}% of the stream (budget ${STREAM_BUSY_SHARE * 100}%)`);
  if (report.stream.longTasks.maxMs > LONG_TASK_MS) problems.push(`a task took ${Math.round(report.stream.longTasks.maxMs)} ms while streaming (budget ${LONG_TASK_MS} ms)`);
  if (report.stream.keys.maxMs > KEY_MS) problems.push(`a key took ${Math.round(report.stream.keys.maxMs)} ms to show while streaming (budget ${KEY_MS} ms)`);
  expect(problems, problems.join('\n')).toEqual([]);
});

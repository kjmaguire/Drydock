import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { TelemetryOtlpChange, TelemetryOtlpStatus, TelemetryStatus } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, errorOf, mount, renderPlain, testServices, type, waitFor } from '../../test/render.tsx';
import { AgentEnvSection } from './agent-env.tsx';
import { NOT_BUILT } from './settings.ts';
import { SettingsScreen, UpdatesSection } from './settings-screen.tsx';
import { TelemetryPanel } from './telemetry.tsx';
import { otlpStatusWords } from './telemetry-otlp.tsx';

afterEach(() => {
  cleanup();
  document.documentElement.removeAttribute('data-theme');
  document.documentElement.style.removeProperty('--bg-app');
});

const tid = (root: ParentNode, id: string) => {
  const el = root.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  if (!el) throw new Error(`no ${id}`);
  return el;
};
const btn = (root: ParentNode, name: string) => byText<HTMLButtonElement>(root, 'button', name);

const tel: TelemetryStatus = {
  enabled: false,
  spans: 0,
  metrics: 0,
  pending: 0,
  droppedAttributeKeys: null,
  destination: 'this device only (local SQLite); export on demand',
  networkExporter: false,
};

const settingsClient = (over: Partial<Client> = {}): Partial<Client> => ({
  appInfo: async () => ({ name: 'Drydock', user: 'kyle', human: 'human:kyle', dataDir: 'C:\\Users\\kyle\\AppData\\Local\\Drydock' }),
  projectList: async () => [],
  licenseStatus: async () => null as never,
  agentEnvGet: async () => ({ pass: [], kept: ['PATH'], prefixes: ['CLAUDE_'] }),
  telemetryStatus: async () => tel,
  ...over,
});

describe('SettingsScreen', () => {
  it('renders every section with no project, and says who and where this session is', async () => {
    const s = testServices(settingsClient());
    s.workspace.start();
    const r = mount(<SettingsScreen />, s);
    expect(r.$('h1')!.textContent).toContain('Settings and about');
    await waitFor(() => expect(r.container.textContent).toContain('human:kyle'), r);
    expect(r.container.textContent).toContain('C:\\Users\\kyle\\AppData\\Local\\Drydock');
    expect(r.container.textContent).toContain('fake');
    expect(tid(r.container, 'stream-status').textContent).toContain('last seq');
    tid(r.container, 'agent-env');
    tid(r.container, 'updates-section');
    await waitFor(() => tid(r.container, 'telemetry-counts'), r);
    // the event stream line follows the store
    s.emit({ type: 'x.y' });
    await waitFor(() => expect(tid(r.container, 'stream-status').textContent).toMatch(/last seq [1-9]\d*/), r);
  });

  it('keeps the "Not built yet" list exactly', async () => {
    const r = mount(<SettingsScreen />, testServices(settingsClient()));
    const table = r.container.querySelector('table[aria-label="Not built yet"]')!;
    const rows = Array.from(table.querySelectorAll('tbody tr')).map((tr) => Array.from(tr.querySelectorAll('td')).map((td) => td.textContent));
    expect(rows).toEqual(NOT_BUILT.map((x) => [x.feature, x.slice, x.meanwhile]));
    expect(Array.from(table.querySelectorAll('th')).map((th) => th.textContent)).toEqual(['Feature', 'Slice', 'Meanwhile']);
    expect(r.container.textContent).toContain('Listed here instead of shown as disabled screens or sample data.');
  });

  it('switches the theme from the swatches and shows the current choice', async () => {
    const s = testServices(settingsClient());
    const r = mount(<SettingsScreen />, s);
    const radio = (v: string) => r.container.querySelector<HTMLInputElement>(`[data-testid="theme-${v}"] input[type="radio"]`)!;
    expect(radio('system').checked).toBe(true);
    expect(tid(r.container, 'theme-dark').textContent).toContain('Dark');
    expect(r.container.querySelector('[role="radiogroup"]')!.getAttribute('aria-label')).toBe('Theme');
    await r.act(() => tid(r.container, 'theme-dark').click());
    expect(s.ui.theme).toBe('dark');
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    await waitFor(() => expect(radio('dark').checked).toBe(true), r);
    await r.act(() => byLabel(r.container, /^Light/).click());
    expect(s.ui.theme).toBe('light');
    await r.act(() => tid(r.container, 'theme-system').click());
    expect(s.ui.theme).toBe('system');
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false);
  });

  it('draws each swatch in its own theme whatever the current one is', () => {
    // the test page has no app stylesheet: define the one token the check reads, the way styles/app.css does
    document.documentElement.style.setProperty('--bg-app', 'light-dark(rgb(250, 250, 250), rgb(20, 20, 24))');
    const r = mount(<SettingsScreen />, testServices(settingsClient()));
    const bg = (v: string, i = 0) => getComputedStyle(tid(r.container, `theme-${v}`).querySelectorAll('[aria-hidden="true"]')[i]!).backgroundColor;
    expect(bg('dark')).toBe('rgb(20, 20, 24)');
    expect(bg('light')).toBe('rgb(250, 250, 250)');
    // System shows both halves: light, then dark
    expect(bg('system', 0)).toBe(bg('light'));
    expect(bg('system', 1)).toBe(bg('dark'));
  });
});

describe('UpdatesSection', () => {
  it('says updates are build-time and untested on Windows, with no buttons', () => {
    const r = renderPlain(<UpdatesSection />);
    const sec = tid(r.container, 'updates-section');
    expect(sec.textContent).toContain('set when Drydock is built');
    expect(sec.textContent).toContain('not been run on Windows');
    expect(sec.textContent).toContain('a backup first');
    expect(sec.querySelectorAll('button').length).toBe(0);
    // the rest (the update channels, backing up by hand) is in the Guide
    expect(Array.from(sec.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toEqual(['/guide/backups']);
  });
});

describe('TelemetryPanel', () => {
  it('is off by default, local only, previews exactly what an export holds, and only a button turns it on', async () => {
    const enables: boolean[] = [];
    const s = testServices({
      telemetryStatus: async () => tel,
      telemetryEnable: async (enabled) => (enables.push(enabled), { ...tel, enabled }),
      telemetryPreview: async () => ({
        schema: 'drydock.telemetry/v1',
        exportedAt: 1,
        resource: { 'service.name': 'drydock' },
        spans: [],
        metrics: [],
        allowlist: ['agent.id', 'task.id'],
        note: 'Contains only allowlisted identifier-shaped attributes.',
      }),
    });
    const r = mount(<TelemetryPanel />, s);
    await waitFor(() => tid(r.container, 'telemetry-counts'), r);
    const box = tid(r.container, 'telemetry');
    // off is quiet (muted); the description says what it does first, then that it is off unless turned on
    expect(tid(r.container, 'telemetry-status').textContent).toBe('off');
    expect(tid(r.container, 'telemetry-status').getAttribute('data-tone')).toBe('muted');
    expect(box.textContent).toContain(
      'Usage counts only: no prompts, code or secrets. Kept on this computer, and sent to a collector only if you set one up. Off unless you turn it on.',
    );
    expect(box.textContent).not.toContain('Off by default');
    expect(box.textContent).toContain('this device only');
    expect(Array.from(box.querySelectorAll('dd')).some((d) => d.textContent === 'none')).toBe(true); // network exporter
    await r.act(() => btn(r.container, 'Preview what an export contains').click());
    const prev = await waitFor(() => tid(r.container, 'telemetry-preview'), r);
    expect(prev.textContent).toContain('Nothing has been recorded');
    expect(prev.textContent).toContain('agent.id, task.id');
    expect(prev.textContent).toContain('allowlisted');
    expect(enables.length).toBe(0);
    await r.act(() => btn(r.container, 'Turn telemetry on').click());
    await waitFor(() => expect(enables).toEqual([true]), r);
  });

  it('turns it off again, says who turned it on, lists dropped keys and exports to a local file', async () => {
    let st: TelemetryStatus = { ...tel, enabled: true, enabledBy: 'human:kyle', spans: 3, metrics: 2, pending: 5, droppedAttributeKeys: { prompt: 2 } };
    const enables: boolean[] = [];
    const r = mount(
      <TelemetryPanel />,
      testServices({
        telemetryStatus: async () => st,
        telemetryEnable: async (enabled) => (enables.push(enabled), (st = { ...st, enabled })),
        telemetryExport: async () => ({ path: 'C:\\data\\telemetry\\t.json', spans: 3, metrics: 2, document: null as never }),
      }),
    );
    await waitFor(() => expect(tid(r.container, 'telemetry-counts').textContent).toBe('3 spans, 2 metrics, 5 not yet exported'), r);
    expect(tid(r.container, 'telemetry').textContent).toContain('turned on by human:kyle');
    expect(tid(r.container, 'telemetry').textContent).toContain('prompt (2)');
    // on is the person's own choice and shown green, not as a warning
    expect(tid(r.container, 'telemetry-status').textContent).toBe('on');
    expect(tid(r.container, 'telemetry-status').getAttribute('data-tone')).toBe('ok');
    await r.act(() => btn(r.container, 'Export to a local file').click());
    expect((await waitFor(() => tid(r.container, 'telemetry-exported'), r)).textContent).toContain('stays on this machine');
    await r.act(() => btn(r.container, 'Turn telemetry off').click());
    await waitFor(() => expect(enables).toEqual([false]), r);
    await waitFor(() => btn(r.container, 'Turn telemetry on'), r);
    expect(tid(r.container, 'telemetry-status').getAttribute('data-tone')).toBe('muted');
  });
});

const otlpOff: TelemetryOtlpStatus = {
  enabled: false,
  endpoint: '',
  encoding: 'protobuf',
  headers: [],
  active: false,
  queued: 0,
  queueSize: 2048,
  sentSpans: 0,
  sentMetrics: 0,
  droppedOverflow: 0,
  droppedFailed: 0,
  rejected: 0,
  canKeepHeaders: true,
};

describe('OTLP export (decision P113)', () => {
  it('sets the endpoint, keeps a header value out of view, turns sending on, and sends a test span', async () => {
    let st: TelemetryStatus = { ...tel, enabled: true, otlp: otlpOff };
    const changes: TelemetryOtlpChange[] = [];
    let tests = 0;
    const apply = (ch: TelemetryOtlpChange): TelemetryStatus => {
      changes.push(ch);
      const o = { ...st.otlp! };
      if (ch.endpoint !== undefined) o.endpoint = ch.endpoint;
      if (ch.encoding) o.encoding = ch.encoding;
      if (ch.enabled !== undefined) o.enabled = ch.enabled;
      if (ch.setHeaders) o.headers = [...(o.headers ?? []), ...Object.keys(ch.setHeaders)];
      if (ch.removeHeaders) o.headers = (o.headers ?? []).filter((h) => !ch.removeHeaders!.includes(h));
      o.active = o.enabled && !!o.endpoint && st.enabled;
      st = { ...st, otlp: o, networkExporter: o.active };
      return st;
    };
    const r = mount(
      <TelemetryPanel />,
      testServices({
        telemetryStatus: async () => st,
        telemetryOtlpSet: async (ch) => apply(ch),
        telemetryOtlpTest: async () => (tests++, { ok: true, status: { ...st.otlp!, lastSuccessAt: Date.UTC(2026, 9, 29, 9, 30) } }),
      }),
    );
    const box = await waitFor(() => tid(r.container, 'otlp'), r);
    expect(tid(r.container, 'otlp-status').textContent).toContain('Not sending: sending is off.');
    expect(btn(r.container, 'Send a test span').disabled).toBe(true); // nothing to send to yet
    await r.act(() => type(byLabel(box, 'Endpoint (OTLP/HTTP)'), 'https://collector.example.com:4318'));
    await r.act(() => type(byLabel<HTMLSelectElement>(box, 'Encoding'), 'json'));
    await r.act(() => btn(box, 'Save endpoint').click());
    await waitFor(() => expect(changes).toEqual([{ endpoint: 'https://collector.example.com:4318', encoding: 'json' }]), r);

    await r.act(() => type(byLabel(box, 'Header name'), 'x-api-key'));
    await r.act(() => type(byLabel(box, 'Header value'), 'secret-value-123'));
    await r.act(() => btn(box, 'Save header').click());
    await waitFor(() => expect(changes[1]).toEqual({ setHeaders: { 'x-api-key': 'secret-value-123' } }), r);
    await waitFor(() => expect(tid(r.container, 'otlp-headers').textContent).toContain('x-api-key'), r);
    expect(tid(r.container, 'otlp-headers').textContent).toContain('value saved');
    expect(box.textContent).not.toContain('secret-value-123');
    expect(byLabel(box, 'Header value').value).toBe('');

    await r.act(() => byLabel<HTMLButtonElement>(box, 'Send to a collector').click());
    await waitFor(() => expect(changes[2]).toEqual({ enabled: true, endpoint: 'https://collector.example.com:4318', encoding: 'json' }), r);
    await waitFor(() => expect(tid(r.container, 'otlp-status').getAttribute('data-active')).toBe('true'), r);
    expect(tid(r.container, 'otlp-status').textContent).toContain('Sending: 0 spans and 0 metrics sent');

    await r.act(() => btn(box, 'Send a test span').click());
    expect((await waitFor(() => tid(r.container, 'otlp-test'), r)).textContent).toContain('The collector accepted the test span');
    expect(tests).toBe(1);

    await r.act(() => btn(box, 'Remove the x-api-key header').click());
    await waitFor(() => expect(changes[3]).toEqual({ removeHeaders: ['x-api-key'] }), r);
  });

  it('shows a failed test and the last error, and never claims to send while telemetry is off', async () => {
    const otlp: TelemetryOtlpStatus = { ...otlpOff, enabled: true, endpoint: 'http://127.0.0.1:4318', lastError: 'the collector answered 401 Unauthorized', lastErrorAt: 1 };
    const r = mount(
      <TelemetryPanel />,
      testServices({
        telemetryStatus: async () => ({ ...tel, otlp }),
        telemetryOtlpTest: async () => ({ ok: false, error: 'the collector answered 401 Unauthorized', status: otlp }),
      }),
    );
    await waitFor(() => tid(r.container, 'otlp-status'), r);
    expect(tid(r.container, 'otlp-status').textContent).toContain('Not sending: telemetry is off, so nothing is recorded or sent.');
    expect(tid(r.container, 'otlp-last-error').textContent).toBe('the collector answered 401 Unauthorized');
    await r.act(() => btn(r.container, 'Send a test span').click());
    expect((await waitFor(() => tid(r.container, 'otlp-test'), r)).textContent).toContain('The test span was not accepted: the collector answered 401 Unauthorized');
  });

  it('words the status line from the counts', () => {
    const on = { ...otlpOff, enabled: true, endpoint: 'https://c', active: true, sentSpans: 12, sentMetrics: 40, queued: 3, droppedOverflow: 1, droppedFailed: 2, rejected: 0 };
    expect(otlpStatusWords(on, true)).toBe(
      'Sending: 12 spans and 40 metrics sent since Drydock started; 3 waiting; 3 dropped (1 queue full, 2 not accepted, 0 rejected by the collector).',
    );
    expect(otlpStatusWords({ ...on, endpoint: '' }, true)).toBe('Not sending: there is no endpoint.');
  });
});

describe('AgentEnvSection', () => {
  /** The section with a stand-in client that records every call of agentEnvSet, whatever it answers. */
  function envSection(setting: (names: string[]) => Promise<void> = async () => undefined) {
    const calls: string[][] = [];
    const r = mount(
      <AgentEnvSection />,
      testServices({
        agentEnvGet: async () => ({ pass: ['AWS_PROFILE'], kept: ['PATH', 'HOME'], prefixes: ['CLAUDE_', 'ANTHROPIC_'] }),
        agentEnvSet: async (names) => {
          calls.push(names);
          await setting(names);
          return { pass: names, kept: ['PATH'], prefixes: ['CLAUDE_'] };
        },
      }),
    );
    const box = byLabel<HTMLTextAreaElement>(r.container, /Also pass these variables/);
    return { r, calls, box };
  }

  it('shows what agents keep and saves names only, trimmed and without blank lines', async () => {
    const { r, calls, box } = envSection();
    await waitFor(() => expect(box.value).toBe('AWS_PROFILE'), r);
    expect(tid(r.container, 'agent-env').textContent).toContain('Agent programs start with a trimmed environment');
    expect(r.container.querySelector('details')!.textContent).toContain('PATH, HOME');
    await r.act(() => type(box, 'AWS_PROFILE\n\n CLAUDE_CODE_USE_BEDROCK '));
    await r.act(() => btn(r.container, 'Save').click());
    await waitFor(() => expect(calls).toEqual([['AWS_PROFILE', 'CLAUDE_CODE_USE_BEDROCK']]), r);
    await waitFor(() => expect(r.container.querySelector('[role="status"]')!.textContent).toContain('Saved'), r);
    // typing again takes the "Saved" back
    await r.act(() => type(box, 'AWS_PROFILE'));
    expect(r.container.querySelector('[role="status"]')).toBe(null);
  });

  it('refuses a pasted NAME=value before anything is sent, says which line it is, and repeats nothing of it anywhere on the page (L38)', async () => {
    const { r, calls, box } = envSection();
    await waitFor(() => expect(box.value).toBe('AWS_PROFILE'), r);
    // nothing the person typed goes to the console either
    const logged: string[] = [];
    const real = { log: console.log, info: console.info, warn: console.warn, error: console.error, debug: console.debug };
    for (const k of Object.keys(real) as (keyof typeof real)[]) console[k] = (...a: unknown[]) => void logged.push(a.map(String).join(' '));
    try {
      await r.act(() => type(box, 'AWS_PROFILE\nAWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG'));
      await r.act(() => btn(r.container, 'Save').click());
      await waitFor(() => expect(errorOf(box)).toBe('Line 2 is not a variable name. Give names only, never values.'), r);
    } finally {
      Object.assign(console, real);
    }
    // the core was not asked, the keyboard is on the box, and nothing says "Saved"
    expect(calls).toEqual([]);
    expect(document.activeElement).toBe(box);
    expect(r.container.querySelector('[role="status"]')).toBe(null);
    // no message, title, label or other attribute anywhere on the page holds any part of the line (the box itself holds what was typed)
    const holds = (needle: string) =>
      Array.from(r.container.querySelectorAll('*')).filter(
        (el) => el !== box && (Array.from(el.attributes).some((a) => a.value.includes(needle)) || (el.children.length === 0 && (el.textContent ?? '').includes(needle))),
      );
    const pieces = ['AWS_SECRET', 'ACCESS_KEY', 'wJalr', 'MDENG', 'K7MDENG'];
    expect(pieces.map((piece) => ({ piece, found: holds(piece).map((el) => el.outerHTML) }))).toEqual(pieces.map((piece) => ({ piece, found: [] })));
    expect(logged.filter((l) => l.includes('wJalr') || l.includes('AWS_SECRET'))).toEqual([]);
    expect(document.title).not.toContain('wJalr');
  });

  it('counts the lines as the box shows them, blank ones included, and a corrected box is sent as it is', async () => {
    const { r, calls, box } = envSection();
    await waitFor(() => expect(box.value).toBe('AWS_PROFILE'), r);
    await r.act(() => type(box, 'AWS_PROFILE\n\n  \nAWS_PROFILE=work'));
    await r.act(() => btn(r.container, 'Save').click());
    await waitFor(() => expect(errorOf(box)).toBe('Line 4 is not a variable name. Give names only, never values.'), r);
    // no message (alert or status) and no title or label holds "work", the value that was pasted (the page's own prose has the word)
    const messages = Array.from(r.container.querySelectorAll('[role="alert"], [role="status"]')).map((el) => el.textContent ?? '');
    const names = Array.from(r.container.querySelectorAll('[title], [aria-label]')).flatMap((el) => [el.getAttribute('title') ?? '', el.getAttribute('aria-label') ?? '']);
    expect([...messages, ...names].filter((x) => x.includes('work'))).toEqual([]);
    expect(r.container.textContent).not.toContain('AWS_PROFILE=work');
    expect(calls).toEqual([]);
    // typing takes the message away at once; the corrected names go to the core
    await r.act(() => type(box, 'AWS_PROFILE\n\n  \nAWS_REGION'));
    expect(errorOf(box)).toBe(null);
    expect(r.container.querySelector('[role="alert"]')).toBe(null);
    await r.act(() => btn(r.container, 'Save').click());
    await waitFor(() => expect(calls).toEqual([['AWS_PROFILE', 'AWS_REGION']]), r);
    await waitFor(() => expect(r.container.querySelector('[role="status"]')!.textContent).toContain('Saved'), r);
  });

  it("still shows the core's own refusal when it has one", async () => {
    const { r, calls, box } = envSection(async () => {
      throw new Error('the settings could not be written');
    });
    await waitFor(() => expect(box.value).toBe('AWS_PROFILE'), r);
    await r.act(() => type(box, 'AWS_PROFILE\nAWS_REGION'));
    await r.act(() => btn(r.container, 'Save').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('the settings could not be written'), r);
    expect(calls).toEqual([['AWS_PROFILE', 'AWS_REGION']]);
    expect(errorOf(box)).toBe(null);
    // a line the page refuses is the only thing it says then: the core's earlier answer is not left beside it
    await r.act(() => type(box, 'AWS_PROFILE\nAWS_REGION=eu'));
    await r.act(() => btn(r.container, 'Save').click());
    await waitFor(() => expect(errorOf(box)).toBe('Line 2 is not a variable name. Give names only, never values.'), r);
    expect(Array.from(r.container.querySelectorAll('[role="alert"]')).map((x) => x.textContent)).toEqual(['Line 2 is not a variable name. Give names only, never values.']);
    expect(calls.length).toBe(1);
  });
});

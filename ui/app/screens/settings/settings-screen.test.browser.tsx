import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { TelemetryStatus } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, mount, renderPlain, testServices, type, waitFor } from '../../test/render.tsx';
import { AgentEnvSection } from './agent-env.tsx';
import { NOT_BUILT } from './settings.ts';
import { SettingsScreen, UpdatesSection } from './settings-screen.tsx';
import { TelemetryPanel } from './telemetry.tsx';

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
    expect(r.$('h1')!.textContent).toContain('Settings and About');
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
    expect(r.container.textContent).toContain('They are listed here instead of shown as disabled screens or sample data.');
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
    expect(sec.textContent).toContain('configured when Drydock is built');
    expect(sec.textContent).toContain('not been run on Windows');
    expect(sec.textContent).toContain('backup before every update');
    expect(sec.querySelectorAll('button').length).toBe(0);
    expect(Array.from(sec.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toEqual(['/license', '/backups']);
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
    expect(Array.from(box.querySelectorAll('span')).some((x) => x.textContent === 'off')).toBe(true);
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
    await r.act(() => btn(r.container, 'Export to a local file').click());
    expect((await waitFor(() => tid(r.container, 'telemetry-exported'), r)).textContent).toContain('stays on this machine');
    await r.act(() => btn(r.container, 'Turn telemetry off').click());
    await waitFor(() => expect(enables).toEqual([false]), r);
    await waitFor(() => btn(r.container, 'Turn telemetry on'), r);
  });
});

describe('AgentEnvSection', () => {
  it('shows what agents keep, saves names only and shows the core refusal of a value', async () => {
    let fail = false;
    const sets: string[][] = [];
    const r = mount(
      <AgentEnvSection />,
      testServices({
        agentEnvGet: async () => ({ pass: ['AWS_PROFILE'], kept: ['PATH', 'HOME'], prefixes: ['CLAUDE_', 'ANTHROPIC_'] }),
        agentEnvSet: async (names) => {
          if (fail) throw new Error('"AWS_PROFILE=work" is not an environment variable name (give names only, never values)');
          sets.push(names);
          return { pass: names, kept: ['PATH'], prefixes: ['CLAUDE_'] };
        },
      }),
    );
    const box = byLabel<HTMLTextAreaElement>(r.container, /Also pass these variables/);
    await waitFor(() => expect(box.value).toBe('AWS_PROFILE'), r);
    expect(tid(r.container, 'agent-env').textContent).toContain('Agent programs start with a trimmed environment');
    expect(r.container.querySelector('details')!.textContent).toContain('PATH, HOME');
    await r.act(() => type(box, 'AWS_PROFILE\n\n CLAUDE_CODE_USE_BEDROCK '));
    await r.act(() => btn(r.container, 'Save').click());
    await waitFor(() => expect(sets).toEqual([['AWS_PROFILE', 'CLAUDE_CODE_USE_BEDROCK']]), r);
    await waitFor(() => expect(r.container.querySelector('[role="status"]')!.textContent).toContain('Saved'), r);
    fail = true;
    await r.act(() => type(box, 'AWS_PROFILE=work'));
    expect(r.container.querySelector('[role="status"]')).toBe(null);
    await r.act(() => btn(r.container, 'Save').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('names only'), r);
  });
});

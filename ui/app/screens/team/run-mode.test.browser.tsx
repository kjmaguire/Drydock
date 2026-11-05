import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { AdapterInfo, Agent, AgentUpdate, Project } from '../../api/types.ts';
import type { WSLStatus } from '../../api/wslClient.ts';
import { lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { AgentEditor } from './agent-panel.tsx';
import { TeamScreen } from './team-screen.tsx';

afterEach(cleanup);

// claude can run in WSL on this (pretend) machine; opencode cannot
const claude: AdapterInfo = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready' },
  capabilities: { modelSelection: true, effortControl: true, runModes: ['native', 'wsl'] } as AdapterInfo['capabilities'],
};
const opencode: AdapterInfo = {
  id: 'opencode',
  detection: { adapter: 'opencode', installed: true, status: 'ready' },
  capabilities: { modelSelection: true, runModes: ['native'] } as AdapterInfo['capabilities'],
};
const distros: WSLStatus = {
  available: true,
  distros: [
    { name: 'Ubuntu', default: true, probed: true, claude: '/home/k/.local/bin/claude', signedIn: true, interop: true },
    { name: 'Debian', probed: true, claude: '', interop: true },
  ],
};
const agent = (over: Partial<Agent> = {}): Agent => ({
  id: 'agt_1',
  projectId: 'prj_1',
  name: 'Pay',
  adapter: 'claude',
  role: 'backend',
  model: '',
  effort: '',
  providerId: '',
  permissionMode: 'acceptEdits',
  status: 'STOPPED',
  ...over,
});

function show(agents: Agent[], client: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    agentList: async () => agents,
    presenceList: async () => [],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    taskList: async () => [],
    adapterList: async () => [claude, opencode],
    agentTemplateList: async () => [],
    skillList: async () => [],
    wslDistros: async () => distros,
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return mount(<TeamScreen onNavigate={() => {}} />, s);
}

/** Opens the New agent dialog (the form is in it) and waits for its adapters. */
async function openNew(r: ReturnType<typeof show>) {
  const btn = await waitFor(() => {
    const b = byText<HTMLButtonElement>(r.container, 'button', /^New agent/);
    if (b.disabled) throw new Error('no project yet');
    return b;
  }, r);
  await r.act(() => btn.click());
  // New agent opens on Describe; the full form is Configure (decision P138)
  const tab = await waitFor(() => r.$('dialog[open] [data-testid="new-agent-configure"]') ?? (() => { throw new Error('no tabs'); })(), r);
  await r.act(() => (tab as HTMLElement).click());
  await waitFor(() => r.$('dialog[open] [data-testid="agent-form"]') ?? (() => { throw new Error('no form'); })(), r);
}

/** The editor of an agent, as the agent's page opens it. */
function edit(a: Agent, client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    agentList: async () => [a],
    presenceList: async () => [],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    taskList: async () => [],
    skillList: async () => [],
    wslDistros: async () => distros,
    ...client,
  });
  s.workspace.start();
  s.project.start();
  let done = 0;
  const r = mount(<AgentEditor agent={a} adapter={claude} caps="ready" onDone={() => done++} onCancel={() => {}} />, s);
  return { r, done: () => done };
}

describe('where an agent runs', () => {
  it('the new-agent form offers Windows or a WSL distro for Claude Code, says what the distro lacks, and sends the choice', async () => {
    const calls: unknown[] = [];
    const r = show([], { agentCreate: async (spec) => (calls.push(spec), { ...agent(), ...spec } as Agent) });
    await openNew(r);
    const runs = await waitFor(() => {
      const sel = byLabel<HTMLElement>(r.container, 'Runs');
      if (optionsOf(sel).length < 4) throw new Error('no distros yet');
      return sel;
    }, r);
    expect(optionsOf(runs).map((o) => o.label)).toEqual([
      'As the project says',
      'On Windows',
      'In WSL: Ubuntu (default distro)',
      'In WSL: Debian - Claude Code not found there',
    ]);
    type(byLabel(r.container, 'Agent name'), 'Linux');
    type(byLabel(r.container, 'Role'), 'backend');
    type(runs, 'wsl:Ubuntu');
    r.root.flush();
    expect(r.container.textContent).toContain('one inside the distro is fast');
    await r.act(() => byText(r.container, 'button', 'Create agent').click());
    await waitFor(() => expect(calls).toEqual([{ projectId: 'prj_1', name: 'Linux', role: 'backend', purpose: '', adapter: 'claude', model: '', effort: '', practices: true, runMode: 'wsl', wslDistro: 'Ubuntu' }]), r);
    // an adapter that cannot run in WSL shows no such choice
    await openNew(r);
    type(byLabel<HTMLElement>(r.container, 'Adapter'), 'opencode');
    r.root.flush();
    expect(r.$('[data-testid="run-mode"]')).toBe(null);
  });

  it('the card names the CLI inside the distro; the editor shows where the agent runs and sends only a change of it', async () => {
    const pay = agent({ runMode: 'wsl', wslDistro: 'Ubuntu' });
    const page = show([pay]);
    const card = await waitFor(() => page.$('[data-testid="agent"][data-name="Pay"]') ?? (() => { throw new Error('no card'); })(), page);
    expect(card.querySelector('[data-testid="agent-runs"]')!.textContent).toBe('Claude Code WSL · backend');
    cleanup();
    const calls: Record<string, unknown>[] = [];
    const update = async (u: AgentUpdate) => (calls.push(u as unknown as Record<string, unknown>), { ...agent(), ...u } as Agent);
    const open = async (r: ReturnType<typeof edit>['r']) => {
      const ed = await waitFor(() => r.$('[data-testid="agent-editor"]') ?? (() => { throw new Error('no editor'); })(), r);
      const runs = await waitFor(() => {
        const sel = byLabel<HTMLElement>(ed, 'Runs');
        if (optionsOf(sel).length < 4) throw new Error('no distros yet');
        return sel;
      }, r);
      return { ed, runs };
    };
    const first = edit(pay, { agentUpdate: update });
    const { ed, runs } = await open(first.r);
    expect(valueOf(runs)).toBe('wsl:Ubuntu');
    await first.r.act(() => byText(ed, 'button', 'Save').click());
    await waitFor(() => expect(calls.length).toBe(1), first.r);
    expect('runMode' in calls[0]!).toBe(false);
    cleanup();
    const second = edit(pay, { agentUpdate: update });
    const two = await open(second.r);
    type(two.runs, 'native');
    second.r.root.flush();
    await second.r.act(() => byText(two.ed, 'button', 'Save').click());
    await waitFor(() => expect(calls.length).toBe(2), second.r);
    expect(calls[1]).toMatchObject({ id: 'agt_1', runMode: 'native', wslDistro: '' });
  });

  it('says so when WSL has no distro, and offers nothing in WSL', async () => {
    const r = show([], { wslDistros: async () => ({ available: true, distros: [], message: '' }) });
    await openNew(r);
    await waitFor(() => expect(r.container.textContent).toContain('No WSL distro is installed on this machine.'), r);
    expect(optionsOf(byLabel(r.container, 'Runs')).map((o) => o.value)).toEqual(['', 'native']);
  });
});

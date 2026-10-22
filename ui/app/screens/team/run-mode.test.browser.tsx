import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { AdapterInfo, Agent, Project } from '../../api/types.ts';
import type { WSLStatus } from '../../api/wslClient.ts';
import { lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
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

describe('where an agent runs', () => {
  it('the new-agent form offers Windows or a WSL distro for Claude Code, says what the distro lacks, and sends the choice', async () => {
    const calls: unknown[] = [];
    const r = show([], { agentCreate: async (spec) => (calls.push(spec), { ...agent(), ...spec } as Agent) });
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
    type(byLabel<HTMLElement>(r.container, 'Adapter'), 'opencode');
    r.root.flush();
    expect(r.$('[data-testid="run-mode"]')).toBe(null);
  });

  it('the editor shows where the agent runs and sends only a change of it', async () => {
    const calls: Record<string, unknown>[] = [];
    const r = show([agent({ runMode: 'wsl', wslDistro: 'Ubuntu' })], { agentUpdate: async (u) => (calls.push(u as unknown as Record<string, unknown>), { ...agent(), ...u } as Agent) });
    const card = await waitFor(() => {
      const c = r.$('[data-testid="agent"][data-name="Pay"]');
      if (!c) throw new Error('no card');
      return c;
    }, r);
    expect(card.querySelector('[data-testid="agent-runs"]')!.textContent).toBe('in WSL');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
    const ed = await waitFor(() => {
      const e = r.$('[data-testid="agent-editor"]');
      if (!e) throw new Error('no editor');
      return e;
    }, r);
    const runs = await waitFor(() => {
      const sel = byLabel<HTMLElement>(ed, 'Runs');
      if (optionsOf(sel).length < 4) throw new Error('no distros yet');
      return sel;
    }, r);
    expect(valueOf(runs)).toBe('wsl:Ubuntu');
    await r.act(() => byText(ed, 'button', 'Save changes').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect('runMode' in calls[0]!).toBe(false);
    await r.act(() => byText(r.$('[data-testid="agent"][data-name="Pay"]')!, 'button', 'Edit Pay').click());
    const ed2 = await waitFor(() => {
      const e = r.$('[data-testid="agent-editor"]');
      if (!e) throw new Error('no editor');
      return e;
    }, r);
    const runs2 = await waitFor(() => {
      const sel = byLabel<HTMLElement>(ed2, 'Runs');
      if (optionsOf(sel).length < 4) throw new Error('no distros yet');
      return sel;
    }, r);
    type(runs2, 'native');
    r.root.flush();
    await r.act(() => byText(ed2, 'button', 'Save changes').click());
    await waitFor(() => expect(calls.length).toBe(2), r);
    expect(calls[1]).toMatchObject({ id: 'agt_1', runMode: 'native', wslDistro: '' });
  });

  it('says so when WSL has no distro, and offers nothing in WSL', async () => {
    const r = show([], { wslDistros: async () => ({ available: true, distros: [], message: '' }) });
    await waitFor(() => expect(r.container.textContent).toContain('No WSL distro is installed on this machine.'), r);
    expect(optionsOf(byLabel(r.container, 'Runs')).map((o) => o.value)).toEqual(['', 'native']);
  });
});

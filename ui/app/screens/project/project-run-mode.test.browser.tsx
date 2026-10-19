import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ProjectRunMode, WSLStatus } from '../../api/wslClient.ts';
import { byLabel, cleanup, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { RunModePanel } from './project-run-mode.tsx';

afterEach(cleanup);

const distros: WSLStatus = { available: true, distros: [{ name: 'Ubuntu', default: true, probed: false, interop: true }] };

function show(client: Partial<Client>) {
  return mount(<RunModePanel projectId="prj_1" />, testServices(client));
}

describe('Where agents run (project settings)', () => {
  it('sets the project default to a WSL distro, and shows the core refusal (the sandbox) when there is one', async () => {
    const calls: string[] = [];
    let current: ProjectRunMode = { runMode: 'native' };
    let refuse = false;
    const r = show({
      wslDistros: async () => distros,
      projectRunMode: async () => current,
      projectSetRunMode: async (id: string, mode: string, d?: string) => {
        calls.push(`${id}:${mode}:${d ?? ''}`);
        if (refuse) throw new Error('this project runs its agents’ commands in a sandbox');
        current = { runMode: mode, wslDistro: d };
        return current;
      },
    });
    const sel = await waitFor(() => {
      const s = byLabel<HTMLElement>(r.container, 'Agents run');
      if (optionsOf(s).length < 2) throw new Error('no distros yet');
      return s;
    }, r);
    expect(optionsOf(sel).map((o) => o.label)).toEqual(['On Windows', 'In WSL: Ubuntu (default distro)']);
    expect(r.container.textContent).toContain('A repository on a Windows drive is slower to read');
    type(sel, 'wsl:Ubuntu');
    await waitFor(() => expect(calls).toEqual(['prj_1:wsl:Ubuntu']), r);
    await waitFor(() => expect(valueOf(byLabel(r.container, 'Agents run'))).toBe('wsl:Ubuntu'), r); // the answer is shown before the next choice
    refuse = true;
    type(byLabel<HTMLElement>(r.container, 'Agents run'), 'native');
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('sandbox'), r);
  });

  it('says when Drydock chose the default from the Connections, and why (P58)', async () => {
    const r = show({
      wslDistros: async () => ({ available: true, distros: [{ name: 'Ubuntu-26.04', default: true, probed: true, interop: true, claude: '/home/k/.local/bin/claude' }] }),
      projectRunMode: async () => ({ runMode: 'wsl', wslDistro: 'Ubuntu-26.04', by: 'drydock', why: 'Claude Code is not usable on Windows; it is ready in the WSL distro Ubuntu-26.04' }),
    });
    await waitFor(() => expect(r.$('[data-testid="run-mode-auto"]')!.textContent).toContain('Drydock chose this when the project was made: Claude Code is not usable on Windows'), r);
    expect(valueOf(byLabel(r.container, 'Agents run'))).toBe('wsl:Ubuntu-26.04');
  });

  it('is not shown where WSL has no distro', async () => {
    const r = show({ wslDistros: async () => ({ available: true, distros: [] }), projectRunMode: async () => ({ runMode: 'native' }) });
    await new Promise((res) => setTimeout(res, 50));
    r.root.flush();
    expect(r.$('[data-testid="run-mode-panel"]')).toBe(null);
  });
});

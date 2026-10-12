import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ProjectRunMode, WSLStatus } from '../../api/wslClient.ts';
import { byLabel, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
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
      const s = byLabel<HTMLSelectElement>(r.container, 'Agents run');
      if (s.options.length < 2) throw new Error('no distros yet');
      return s;
    }, r);
    expect(Array.from(sel.options).map((o) => o.textContent)).toEqual(['On Windows', 'In WSL: Ubuntu (default distro)']);
    expect(r.container.textContent).toContain('A repository on a Windows drive is read across the WSL file share');
    type(sel, 'wsl:Ubuntu');
    await waitFor(() => expect(calls).toEqual(['prj_1:wsl:Ubuntu']), r);
    refuse = true;
    type(byLabel<HTMLSelectElement>(r.container, 'Agents run'), 'native');
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('sandbox'), r);
  });

  it('is not shown where WSL has no distro', async () => {
    const r = show({ wslDistros: async () => ({ available: true, distros: [] }), projectRunMode: async () => ({ runMode: 'native' }) });
    await new Promise((res) => setTimeout(res, 50));
    r.root.flush();
    expect(r.$('[data-testid="run-mode-panel"]')).toBe(null);
  });
});

// Where the project's agents run unless an agent chooses (internal/core/ext_wsl.go, decision P41): on Windows, or inside a WSL
// distro. Shown only where WSL is installed. The core refuses WSL while the project's sandbox is on, and says why. A new
// project's default comes from the Connections (decision P58): when Drydock chose it, the panel says so and why.

import type { Handle } from 'remix/component';
import type { ProjectRunMode, WSLStatus } from '../../api/wslClient.ts';
import { action, query, services } from '../../state/services.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Select } from '../../ui/select.tsx';
import { distroLabel, parseRunMode, runModeValue, WSL_HELP } from '../team/run-mode.ts';
import { SettingRow, SettingsGroup, errorPad, noteRow } from './settings-kit.tsx';

const noWSL: WSLStatus = { available: false, distros: [] };

export function RunModePanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const wslQ = query(handle, {
    load: async () => {
      try {
        return await s.client.wslDistros();
      } catch {
        return noWSL;
      }
    },
  });
  const q = query(handle, {
    load: (): Promise<ProjectRunMode> => s.client.projectRunMode(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type === 'project.run_mode',
  });
  const act = action(handle);
  const choose = async (v: string) => {
    const r = parseRunMode(v);
    if (await act.run('set', () => s.client.projectSetRunMode(handle.props.projectId, r.runMode || 'native', r.wslDistro))) q.reload();
  };
  return () => {
    const st = wslQ.data;
    if (!st || !st.available || st.distros.length === 0) return null;
    const pr = q.data;
    const value = pr ? runModeValue(pr.runMode, pr.wslDistro) : 'native';
    const id = `runmode-${handle.id}`;
    return (
      <SettingsGroup
        title="Where agents run"
        label="Where agents run"
        testId="run-mode-panel"
        description="Claude Code agents can run on Windows or inside a WSL distro. An agent can choose for itself in its settings; the others follow this."
      >
        {act.error || q.error ? (
          <div mix={errorPad}>
            <ErrorNote error={act.error ?? q.error} what="Not changed" />
          </div>
        ) : null}
        <SettingRow label="Agents run" for={id}>
          <Select
            id={id}
            value={value}
            disabled={!!act.busy || !pr}
            options={[{ value: 'native', label: 'On Windows' }, ...st.distros.map((d) => ({ value: `wsl:${d.name}`, label: distroLabel(d) }))]}
            onChange={(v) => void choose(v)}
          />
        </SettingRow>
        {pr?.by === 'drydock' ? (
          <p mix={noteRow} data-testid="run-mode-auto">
            Drydock chose this when the project was made{pr.why ? `: ${pr.why}` : ''}. Choose another place above to change it.
          </p>
        ) : null}
        <p mix={noteRow}>{WSL_HELP}</p>
      </SettingsGroup>
    );
  };
}

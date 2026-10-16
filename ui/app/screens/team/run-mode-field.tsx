// The "Runs" field of the agent forms: on Windows, inside a WSL distro, or as the project says (decision P41). Shown only for an
// adapter that can start its CLI inside a distro on this machine.

import { css, type Handle } from 'remix/component';
import type { Capabilities } from '../../api/types.ts';
import type { WSLStatus } from '../../api/wslClient.ts';
import { query, services } from '../../state/services.tsx';
import { faint } from '../../ui/data.tsx';
import { Field } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { distroLabel, parseRunMode, runModeValue, supportsWSL, WSL_HELP } from './run-mode.ts';

const full = css({ gridColumn: '1 / -1', display: 'grid', gap: '6px', '& p': { margin: '0' } });

const noWSL: WSLStatus = { available: false, distros: [] };

export function RunModeField(
  handle: Handle<{ caps: Capabilities | undefined; runMode: string; wslDistro: string; onChange: (runMode: string, wslDistro: string) => void }>,
) {
  const s = services(handle);
  const q = query(handle, {
    enabled: () => supportsWSL(handle.props.caps),
    load: async () => {
      try {
        return await s.client.wslDistros();
      } catch {
        return noWSL; // a core without the WSL methods: offer nothing
      }
    },
  });
  return () => {
    const { caps, runMode, wslDistro, onChange } = handle.props;
    if (!supportsWSL(caps)) return null;
    const st = q.data ?? noWSL;
    const value = runModeValue(runMode, wslDistro);
    const known = st.distros.some((d) => `wsl:${d.name}` === value);
    return (
      <div mix={full} data-testid="run-mode">
        <Field label="Runs">
          {(id) => (
            <Select
              id={id}
              label="Runs"
              value={value}
              options={[
                { value: '', label: 'As the project says' },
                { value: 'native', label: 'On Windows' },
                ...st.distros.map((d) => ({ value: `wsl:${d.name}`, label: distroLabel(d) })),
                ...(value.startsWith('wsl:') && !known ? [{ value, label: `In WSL: ${wslDistro}`, hint: 'not found on this machine' }] : []),
              ]}
              onChange={(v) => {
                const r = parseRunMode(v);
                onChange(r.runMode, r.wslDistro);
              }}
            />
          )}
        </Field>
        {q.data && st.distros.length === 0 ? <p mix={faint}>{st.message || 'No WSL distro is installed on this machine.'}</p> : null}
        {value.startsWith('wsl:') ? <p mix={faint}>{WSL_HELP}</p> : null}
      </div>
    );
  };
}

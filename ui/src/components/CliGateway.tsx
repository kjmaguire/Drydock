import type { McpAdapterRow } from '../api/mcpClient';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { useReadOnly } from '../state/license';
import { Badge, ErrorNote } from './common';

/**
 * Which agent CLIs get the workspace tools and the MCP servers you give them. Claude Code is proven with real runs. For the
 * others the setting is off until you allow it, because no real run has shown that the CLI honours it yet.
 */
export function CliGateway() {
  const client = useClient();
  const { readOnly } = useReadOnly();
  const status = useLive(() => client.mcpStatus(), [], (e) => e.type.startsWith('mcp.'));
  const act = useAction();
  const rows = status.data?.adapters ?? [];
  const toggle = async (r: McpAdapterRow, on: boolean) => {
    if (await act.run(`try:${r.adapter}`, () => client.mcpTrySet(r.adapter, on))) status.reload();
  };
  return (
    <section aria-label="Agent CLIs and the MCP gateway">
      <h2>Agent CLIs</h2>
      <p className="muted">
        Servers you give an agent reach it through one local gateway. Claude Code is verified with real runs. For the other CLIs, Drydock hands the gateway over when the session starts, but no real
        run has shown yet that each CLI uses it, so it stays off until you allow it.
      </p>
      <ErrorNote error={status.error ?? act.error} what="Agent CLIs" />
      {status.data && !status.data.running ? <p className="muted">The gateway is not running.</p> : null}
      <ul data-testid="cli-gateway">
        {rows.map((r) => (
          <li key={r.adapter} data-adapter={r.adapter}>
            <strong>{r.adapter}</strong>{' '}
            {r.attached && !r.trying ? <Badge tone="ok">verified</Badge> : null}
            {r.trying ? <Badge tone="warn">allowed by you, not verified</Badge> : null}
            {!r.attached && !r.canTry ? <Badge tone="warn">not available</Badge> : null}
            {r.canTry ? (
              <label className="inline">
                <input
                  type="checkbox"
                  checked={r.trying}
                  disabled={readOnly || act.busy !== ''}
                  onChange={(e) => toggle(r, e.target.checked)}
                  aria-label={`Let ${r.adapter} use the gateway`}
                />{' '}
                Let {r.adapter} use the gateway
              </label>
            ) : null}
            {!r.attached && !r.canTry && r.reason ? <span className="muted"> {r.reason}</span> : null}
          </li>
        ))}
      </ul>
    </section>
  );
}

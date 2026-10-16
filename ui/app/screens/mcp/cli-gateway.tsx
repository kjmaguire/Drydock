// Which agent CLIs get the workspace tools and the MCP servers you give them. Claude Code is proven with real runs. For the
// others the setting is off until you allow it, because no real run has shown that the CLI honours it yet. It sits on the
// Connections page, below the agent CLIs' tiles.

import { css, type Handle } from 'remix/component';
import type { McpAdapterRow } from '../../api/mcpClient.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Avatar, Badge, muted } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { listCard, Section } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';

const rowCss = css({ flexWrap: 'wrap' });
const nameCss = css({ fontWeight: '600', fontFamily: t.mono, fontSize: t.textSm });
const reasonCss = css({ flexBasis: '100%', paddingLeft: '32px', fontSize: t.textSm });
const toggleCss = css({ marginLeft: 'auto' });

export function CliGateway(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const status = query(handle, { load: () => s.client.mcpStatus(), when: (e) => e.type.startsWith('mcp.') });
  const act = action(handle);
  const toggle = async (r: McpAdapterRow, enabled: boolean) => {
    if (await act.run(`try:${r.adapter}`, () => s.client.mcpTrySet(r.adapter, enabled))) status.reload();
  };

  return () => {
    const { readOnly } = s.workspace.license;
    const rows = status.data?.adapters ?? [];
    return (
      <Section
        label="Agent CLIs and the MCP gateway"
        testId="cli-gateway-section"
        title="MCP gateway"
        description="Which agent CLIs receive the MCP servers you give an agent. The servers reach an agent through one local gateway. Claude Code is verified with real runs. For the other CLIs, Drydock hands the gateway over when the session starts, but no real run has shown yet that each CLI uses it, so it stays off until you allow it here."
      >
        <ErrorNote error={status.error ?? act.error} what="MCP gateway" />
        {status.data && !status.data.running ? (
          <Notice tone="warn">The gateway is not running.</Notice>
        ) : null}
        <ul mix={listCard} data-testid="cli-gateway">
          {rows.map((r) => (
            <li key={r.adapter} data-adapter={r.adapter} mix={rowCss}>
              <Avatar id={r.adapter} name={r.adapter} size={22} square kind="system" />
              <span mix={nameCss}>{r.adapter}</span>
              {r.attached && !r.trying ? <Badge tone="ok">verified</Badge> : null}
              {r.trying ? <Badge tone="warn">allowed by you, not verified</Badge> : null}
              {!r.attached && !r.canTry ? <Badge tone="warn">not available</Badge> : null}
              {r.canTry ? (
                <span mix={toggleCss}>
                  <Checkbox label={`Let ${r.adapter} use the gateway`} checked={r.trying} disabled={readOnly || act.busy !== ''} onChange={(c) => void toggle(r, c)} />
                </span>
              ) : null}
              {!r.attached && !r.canTry && r.reason ? <span mix={[reasonCss, muted]}>{r.reason}</span> : null}
            </li>
          ))}
          {status.data && rows.length === 0 ? (
            <li mix={muted}>
              <Avatar id="none" name="CLI" size={22} square kind="system" />
              No agent CLIs reported.
            </li>
          ) : null}
        </ul>
      </Section>
    );
  };
}

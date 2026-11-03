// The fix of what went wrong in the background, in the Inbox item itself (lane B batch B6, F14 and H7): a model provider that is
// down is checked again here, an MCP server whose sign-in expired is signed in again here (the server's own page opens in the
// browser), a scan tool whose install failed is installed again here. A connected service, an agent CLI's output and a crash
// report are fixed on their own pages: the item's Open button goes there (a service opens with Reconnect open). The item goes by
// itself once the core sees the state fine again.

import { css, on, type Handle } from 'remix/component';
import type { NeedsYouItem } from '../../api/types.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { OAuthPanel } from '../mcp/oauth-panel.tsx';

const rowCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2, '& p': { margin: '0' } });

/** The label of an item's Open button for a background problem; '' for another kind. */
export function problemOpenLabel(kind: string): string {
  switch (kind) {
    case 'integration':
      return 'Reconnect';
    case 'provider':
      return 'Open in Models';
    case 'mcp_sign_in':
      return 'Open the server';
    case 'adapter_drift':
      return 'Open on the Agents page';
    case 'tool_install':
      return 'Open Security';
    case 'crash':
      return 'Open Support';
  }
  return '';
}

/** The fix done in place, for the kinds that have one: Check again, Sign in, Retry. */
export function ProblemFix(handle: Handle<{ item: NeedsYouItem }>) {
  const s = services(handle);
  const act = action(handle);
  let said = '';
  const run = async (what: string, f: () => Promise<string>) => {
    said = '';
    const r = await act.run(what, f);
    if (typeof r === 'string') said = r;
    void handle.update();
  };
  // the MCP server, for its sign-in panel (read only for that kind)
  const server = query(handle, {
    load: async () => (handle.props.item.kind === 'mcp_sign_in' ? ((await s.client.mcpLibList()).find((x) => x.id === handle.props.item.subject) ?? null) : null),
    key: () => [handle.props.item.kind, handle.props.item.subject ?? ''],
    when: (e) => e.type.startsWith('mcp.server'),
  });

  return () => {
    const { item: it } = handle.props;
    const subject = it.subject ?? '';
    if (!subject) return null;
    if (it.kind === 'provider')
      return (
        <div mix={rowCss} data-testid="problem-fix">
          <Button
            size="sm"
            disabled={!!act.busy}
            mix={on('click', () =>
              run('check', async () => {
                const r = await s.client.providerTest(subject);
                return r.OK ? 'It answered: this goes from the Inbox.' : `Still not answering${r.Error ? `: ${r.Error}` : '.'}`;
              }),
            )}
          >
            {act.busy === 'check' ? 'Checking...' : 'Check again'}
          </Button>
          {said ? <p data-testid="problem-fix-result">{said}</p> : null}
          <ErrorNote error={act.error} />
        </div>
      );
    if (it.kind === 'tool_install')
      return (
        <div mix={rowCss} data-testid="problem-fix">
          <Button
            size="sm"
            disabled={!!act.busy}
            mix={on('click', () =>
              run('retry', async () => {
                await s.client.securityToolInstall(subject);
                return 'Installing again. The Security page shows how it goes.';
              }),
            )}
          >
            {act.busy === 'retry' ? 'Starting...' : 'Retry'}
          </Button>
          {said ? <p data-testid="problem-fix-result">{said}</p> : null}
          <ErrorNote error={act.error} />
        </div>
      );
    if (it.kind === 'mcp_sign_in') {
      const srv = server.data;
      return srv ? (
        <div data-testid="problem-fix">
          <OAuthPanel server={srv} onChanged={() => server.reload()} />
        </div>
      ) : (
        <ErrorNote error={server.error} />
      );
    }
    return null;
  };
}

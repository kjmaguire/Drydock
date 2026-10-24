// One MCP server you added yourself (a web address or a program on this PC), as its detail on the Integrations page (decision
// P98; it was the Library's server panel). Rows: where it runs, how it is reached, its send guard, its secrets (names only), its
// sign-in, its state and the projects that have it; its tools, where you tick the ones that only read, with Find tools (or Test and
// refresh tools) and the note that says which program that starts; Edit in a dialog, and Remove after a confirmation. A server
// connected from a vendor's card opens here too (its card links "Tools and settings"), so which of its tools only read is set the
// same way. Nothing of a project: a server is added to a project, and granted to its agents, on that project's Integrations page.

import { css, on, type Handle } from 'remix/component';
import { Globe, KeyRound, Pencil, Plug, RefreshCw, Search, Trash2 } from 'lucide';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import { plural } from '../../lib/format.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ClippedText } from '../../ui/clipped-text.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { DetailHeader, SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { t } from '../../ui/tokens.ts';
import { ReachNote } from '../mcp/command-line.tsx';
import { serverLogo } from '../mcp/mcp-screen.tsx';
import { OAuthPanel } from '../mcp/oauth-panel.tsx';
import { McpServerForm } from '../mcp/server-form.tsx';
import { inputOf, whereOf } from '../mcp/server-input.ts';
import { ownServerState } from './integrations.ts';

const detailCss = css({ display: 'grid', gap: t.s5, minWidth: '0', '& p': { margin: '0' } });
const whereCss = css({ display: 'block', overflowWrap: 'anywhere' });
const toolsTable = css({
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: t.textMd,
  '& th': { textAlign: 'left', fontSize: t.textXs, fontWeight: '600', color: t.textMuted, padding: `6px ${t.s4}`, borderBottom: `1px solid ${t.border}` },
  '& td': { padding: `${t.s2} ${t.s4}`, borderBottom: `1px solid ${t.border}`, verticalAlign: 'top' },
  '& tr:last-child td': { borderBottom: '0' },
  '& td > div': { color: t.textMuted, fontSize: t.textSm, marginTop: '2px' },
  '& label': { display: 'inline-flex', alignItems: 'center', gap: t.s2, cursor: 'pointer' },
});
const confirmCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  padding: `${t.s2} ${t.s3}`,
  borderRadius: t.rMd,
  background: t.dangerSoft,
  fontSize: t.textMd,
  '& > span': { flex: '1 1 240px' },
});
const markCss = css({ display: 'inline-flex', verticalAlign: '-2px', marginRight: '6px', color: t.textFaint });
const toolsNote = css({ display: 'grid', gap: t.s2, padding: `${t.s3} ${t.s4}`, borderTop: `1px solid ${t.border}`, '&:first-child': { borderTop: '0' } });

/** The server's detail. `onRemoved` runs once it is gone (the page chooses another row and says so). */
export function ServerDetail(handle: Handle<{ s: McpLibraryServer; onChanged: () => void; onRemoved: (display: string) => void }>) {
  const svc = services(handle);
  watch(handle, svc.workspace);
  const act = action(handle);
  let editing = false;
  let confirm = false;
  // the note beside the button says which program Find tools starts (M24): the button is described by it
  const reachId = `reach-${handle.id}`;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };

  const setReadOnly = async (tool: string, ticked: boolean) => {
    const { s, onChanged } = handle.props;
    const next = new Set(s.readOnlyTools);
    if (ticked) next.add(tool);
    else next.delete(tool);
    await act.run('readonly', () => svc.client.mcpLibSave(inputOf(s, { readOnlyTools: [...next] })));
    onChanged();
  };

  return () => {
    const { s, onChanged, onRemoved } = handle.props;
    const ro = svc.workspace.license;
    const n = s.projects.length;
    const st = ownServerState(s);
    const find = s.tools.length > 0 ? 'Test and refresh tools' : 'Find tools';
    return (
      <article mix={detailCss} data-testid="server-detail" data-server={s.id} aria-label={s.display}>
        <DetailHeader
          title={s.display}
          logo={serverLogo(s) || undefined}
          icon={s.transport === 'http' ? Globe : Plug}
          status={st.bucket === 'on' ? { text: 'Connected', tone: 'ok' } : { text: st.text, tone: st.tone === 'err' ? 'err' : 'warn' }}
          sub={
            <>
              <code mix={faint}>{s.id}</code> · {s.transport === 'http' ? 'web address' : 'program on this PC'}
            </>
          }
          actions={
            <>
              <Button size="sm" variant="ghost" icon={Pencil} aria-expanded={editing} mix={[on('click', () => set(() => (editing = !editing)))]}>
                Edit
              </Button>
              {confirm ? null : (
                <Button size="sm" variant="ghost" icon={Trash2} mix={[on('click', () => set(() => (confirm = true)))]}>
                  Remove
                </Button>
              )}
            </>
          }
        />
        <ErrorNote error={act.error} what="Not changed" />
        {confirm ? (
          <div mix={confirmCss} data-testid="server-remove-confirm">
            <span>
              Remove {s.display}? It leaves {plural(n, 'project')} and its stored secrets are deleted.
            </span>
            <Button
              size="sm"
              variant="danger"
              disabled={act.busy !== ''}
              mix={[
                on('click', async () => {
                  const done = await act.run('delete', async () => {
                    await svc.client.mcpLibDelete(s.id);
                    return true;
                  });
                  if (done) onRemoved(s.display);
                }),
              ]}
            >
              Yes, remove
            </Button>
            <Button size="sm" mix={[on('click', () => set(() => (confirm = false)))]}>
              Keep
            </Button>
          </div>
        ) : null}
        <SettingsGroup title="Server" testId="server-facts">
          <SettingRow label={s.transport === 'http' ? 'Address' : 'Program'}>
            <code mix={[mono, whereCss]}>{whereOf(s)}</code>
          </SettingRow>
          <SettingRow label="Send guard">
            {s.guard ? (
              <Badge tone="info" title="Agents cannot send code, files, secrets or long text to it">
                send guard on
              </Badge>
            ) : (
              <Badge tone="warn">send guard off</Badge>
            )}
          </SettingRow>
          {s.secretEnv.length > 0 || s.hasToken ? (
            <SettingRow label="Secrets">
              <span data-testid="mcp-secrets">
                <span mix={markCss} aria-hidden="true">
                  <Icon icon={KeyRound} size={13} />
                </span>
                Kept in the credential store: {s.hasToken ? 'bearer token' : s.secretEnv.join(', ')} (values are never shown)
              </span>
            </SettingRow>
          ) : null}
          {s.oauth ? (
            <SettingRow label="Sign-in">
              <OAuthPanel server={s} onChanged={onChanged} />
            </SettingRow>
          ) : null}
          <SettingRow label="State">
            <span data-testid="server-state">{st.bucket === 'on' ? `Connected${s.account ? ` as ${s.account}` : ''}` : st.text}</span>
            {s.lastError ? (
              <span mix={faint} style={{ display: 'block' }} data-testid="server-error">
                {s.lastError}
              </span>
            ) : null}
          </SettingRow>
          <SettingRow label="Projects">
            <span mix={muted} data-testid="server-projects">
              {n === 0 ? 'Not in any project yet.' : `In ${n} project${n === 1 ? '' : 's'}.`} Add it to a project, and choose which of its agents may use it, on that project's Integrations
              page.
            </span>
          </SettingRow>
        </SettingsGroup>
        <SettingsGroup
          title="Tools"
          testId="server-tools"
          actions={
            <Button
              size="sm"
              icon={s.tools.length > 0 ? RefreshCw : Search}
              disabled={act.busy !== ''}
              aria-describedby={reachId}
              mix={[
                on('click', async () => {
                  const r = await act.run('discover', () => svc.client.mcpLibDiscover(s.id));
                  if (r) onChanged();
                }),
              ]}
            >
              {act.busy === 'discover' ? 'Connecting...' : find}
            </Button>
          }
        >
          {s.tools.length === 0 ? (
            <div mix={toolsNote}>
              <p mix={muted}>Tools are not known yet. Press Find tools to start the server and list them.</p>
            </div>
          ) : (
            <table mix={toolsTable} aria-label={`Tools of ${s.display}`}>
              <thead>
                <tr>
                  <th scope="col">Tool</th>
                  <th scope="col">Reads only (no approval)</th>
                </tr>
              </thead>
              <tbody>
                {s.tools.map((tool) => (
                  <tr key={tool.name} data-testid="mcp-tool" data-tool={tool.name}>
                    <td>
                      <code>{tool.name}</code>
                      {tool.flags?.length ? (
                        <Badge tone="warn" title={`Instruction-like text found: ${tool.flags.join(', ')}`} testId="mcp-tool-flagged">
                          held back
                        </Badge>
                      ) : null}
                      {tool.flags?.length ? (
                        <div mix={faint}>
                          Its description reads like instructions to the agent ({tool.flags.join(', ')}). Drydock does not grant it to agents; the description is shown here as
                          data.
                        </div>
                      ) : null}
                      {tool.description ? <ClippedText text={tool.description} data-testid="mcp-tool-description" /> : null}
                    </td>
                    <td>
                      <label>
                        <input
                          type="checkbox"
                          aria-label={`${tool.name} only reads`}
                          checked={s.readOnlyTools.includes(tool.name)}
                          disabled={act.busy !== '' || ro.readOnly}
                          mix={on('change', (e) => void setReadOnly(tool.name, e.currentTarget.checked))}
                        />
                        {tool.readOnlyHint ? <span mix={faint}>the server says it only reads</span> : null}
                      </label>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <div mix={toolsNote}>
            <p mix={faint}>Every tool you do not tick asks for your approval, call by call. Ticking says the tool cannot change anything.</p>
            <ReachNote server={s} lead={`Pressing ${find}`} id={reachId} />
          </div>
        </SettingsGroup>
        <Dialog open={editing} title={`Edit ${s.display}`} wide onClose={() => set(() => (editing = false))}>
          {editing ? (
            <McpServerForm
              initial={s}
              onCancel={() => set(() => (editing = false))}
              onDone={() => {
                set(() => (editing = false));
                onChanged();
              }}
            />
          ) : null}
        </Dialog>
      </article>
    );
  };
}


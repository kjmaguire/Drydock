// MCP servers of one project: which connected servers the project has (ServerCard), which of its agents may use each one, the
// tool calls waiting for the human's approval (Approvals) and putting a connected server into the project (AddFromLibrary).
// They sit on the project's own Integrations page (/project/integrations, screens/integrations/project-integrations-screen.tsx);
// servers are connected once for the workspace, in the Library or on the workspace's Integrations page. A server nobody was
// granted is never reached.

import { css, on, type Handle } from 'remix/component';
import { BadgeCheck, FolderPlus, Globe, Plug, Power, ShieldAlert } from 'lucide';
import type { McpApproval, McpLibraryServer, McpServer } from '../../api/mcpClient.ts';
import type { Agent } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { visible } from '../../lib/visible.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { BrandLogo, brandKey } from '../../ui/brand-logo.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge, faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, listCard, Section } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { ShownNotes, Targets } from '../shared/approval-text.tsx';
import { whereOf } from './server-input.ts';

/** The brand a server shows, from the catalog entry it was connected from or its own id; '' when it has none. */
export function serverLogo(s: Pick<McpLibraryServer, 'id' | 'catalogId'>): string {
  return [s.catalogId, s.id].find((x): x is string => !!x && brandKey(x) !== '') ?? '';
}

/** A server's logo when its brand is known, else an icon for how it is reached, on the accent square. */
export function ServerMark(handle: Handle<{ s: Pick<McpLibraryServer, 'id' | 'catalogId' | 'transport'>; size?: number }>) {
  return () => {
    const { s, size = 32 } = handle.props;
    const logo = serverLogo(s);
    return logo ? (
      <BrandLogo id={logo} size={size} />
    ) : (
      <span mix={tile} style={{ width: `${size}px`, height: `${size}px` }}>
        <Icon icon={s.transport === 'http' ? Globe : Plug} size={16} />
      </span>
    );
  };
}

export const cardHead = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  minWidth: '0',
  '& h3': { fontSize: t.textLg, fontWeight: '600', margin: '0' },
});
export const tile = css({
  display: 'grid',
  placeItems: 'center',
  width: '32px',
  height: '32px',
  flex: 'none',
  borderRadius: t.rMd,
  background: t.accentSoft,
  color: t.accentText,
});
export const subhead = css({ fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase', color: t.textMuted, margin: '0' });
export const whereCss = css({ display: 'block', padding: '6px 10px', borderRadius: t.rMd, background: t.bgApp, border: `1px solid ${t.border}`, overflowWrap: 'anywhere' });
export const actionsRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });
export const confirmCss = css({
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
const cardBody = css({ display: 'grid', gap: t.s3 });
const toolList = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': { display: 'inline-flex', alignItems: 'center', gap: '6px', padding: '3px 4px 3px 8px', border: `1px solid ${t.border}`, borderRadius: t.rMd, background: t.bgApp },
});
const agentList = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': { padding: '6px 10px', border: `1px solid ${t.border}`, borderRadius: t.rMd },
});
const grantRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, cursor: 'pointer', minWidth: '0' });
const approvalCard = css({ borderColor: `color-mix(in srgb, ${t.warning} 45%, ${t.border})` });
const argsCss = css({ margin: '0', maxHeight: '240px', overflow: 'auto', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' });
const libRow = css({
  gap: t.s3,
  '& .who': { display: 'grid', flex: '1', minWidth: '0' },
  '& .who strong': { fontWeight: '600' },
  '& .who > span': { fontSize: t.textSm, overflowWrap: 'anywhere' },
});

/** The tool calls of this project's agents that wait for the human; nothing while there are none. */
export function Approvals(handle: Handle<{ projectId: string; agents: readonly Agent[] }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.mcpApprovalList(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type.startsWith('approval.') || e.type.startsWith('mcp.'),
  });
  const decide = async (a: McpApproval, approve: boolean) => {
    await act.run(approve ? 'approve' : 'reject', () => s.client.mcpApprovalDecide(a.id, approve));
    q.reload();
  };
  return () => {
    const rows = q.data ?? [];
    if (rows.length === 0) return null;
    const ro = s.workspace.license;
    const name = (id: string) => handle.props.agents.find((a) => a.id === id)?.name ?? id;
    return (
      <Section
        label="Calls waiting for you"
        testId="mcp-approvals"
        title="Waiting for your approval"
        description="An agent called a tool that is not marked read-only. The call has not been made. Approve only the exact call shown."
      >
        <ErrorNote error={act.error} what="Not sent" />
        {rows.map((a) => (
          <article key={a.id} mix={[card, cardBody, approvalCard]} data-testid="mcp-approval" data-approval={a.id}>
            <header mix={cardHead}>
              <span mix={tile} style={{ background: 'var(--warning-soft)', color: 'var(--warning)' }}>
                <Icon icon={ShieldAlert} size={16} />
              </span>
              <h3>
                <code>{visible(a.server)}</code> / <code>{visible(a.tool)}</code>
              </h3>
              <span mix={faint}>
                {name(a.agentId)}, asked {formatDateTime(a.created)}
              </span>
            </header>
            <p mix={[muted]} style={{ margin: '0' }}>
              Arguments the agent sent (text from an agent, shown as is):
            </p>
            <Targets targets={a.targets} testId="mcp-approval-targets" />
            <ShownNotes shortened={a.shortened} texts={[a.args, a.server, a.tool, ...(a.targets ?? [])]} testId="mcp-approval" />
            <pre mix={argsCss} tabIndex={0}>
              {visible(a.args)}
            </pre>
            <div mix={actionsRow}>
              <Button
                variant="primary"
                icon={BadgeCheck}
                disabled={act.busy !== '' || ro.readOnly}
                title={ro.readOnly ? ro.reason : undefined}
                mix={[on('click', () => void decide(a, true))]}
              >
                Approve this call
              </Button>
              <Button disabled={act.busy !== ''} mix={[on('click', () => void decide(a, false))]}>
                Reject
              </Button>
            </div>
          </article>
        ))}
      </Section>
    );
  };
}

/** One connected server as this project has it: its tools, which agents may use it, on or off here, take it out. */
export function ServerCard(handle: Handle<{ s: McpServer; agents: readonly Agent[]; onChanged: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let confirmRemove = false;
  // a grant ticked or unticked shows at once; the server's answer replaces it
  const want = new Map<string, boolean>();

  return () => {
    const { s: srv, agents, onChanged } = handle.props;
    const ro = s.workspace.license;
    const granted = (agentId: string) => srv.agents.some((a) => a.agentId === agentId);
    for (const [a, v] of want) if (granted(a) === v) want.delete(a); // the server agrees now
    return (
      <article mix={[card, cardBody]} data-testid="mcp-server" data-server={srv.id}>
        <header mix={cardHead}>
          <ServerMark s={srv} />
          <h3>{srv.display}</h3>
          <code mix={faint}>{srv.id}</code>
          <Badge tone={srv.enabled ? 'ok' : 'muted'}>{srv.enabled ? 'on' : 'off in this project'}</Badge>
          <Badge title="Where it runs">{srv.transport === 'http' ? 'web address' : 'program'}</Badge>
          {srv.guard ? (
            <Badge tone="info" title="Agents cannot send code, files, secrets or long text to it">
              send guard on
            </Badge>
          ) : (
            <Badge tone="warn">send guard off</Badge>
          )}
          {srv.projects.length > 1 ? (
            <Badge title="The same connected server is in several projects; each project has its own agents and grants">in {srv.projects.length} projects</Badge>
          ) : null}
        </header>
        <ErrorNote error={act.error} what="Not changed" />
        <code mix={[mono, whereCss]}>{whereOf(srv)}</code>

        <h4 mix={subhead}>Tools</h4>
        {srv.tools.length === 0 ? (
          <p mix={muted} style={{ margin: '0' }}>
            Tools are not known yet. Open the <a href={routes.library.href()}>Library</a>, choose the server and press Find tools.
          </p>
        ) : (
          <ul mix={toolList} aria-label={`Tools of ${srv.display}`}>
            {srv.tools.map((tool) => (
              <li key={tool.name} data-testid="mcp-tool" data-tool={tool.name}>
                <code>{tool.name}</code> {srv.readOnlyTools.includes(tool.name) ? <Badge tone="ok">reads only</Badge> : <Badge tone="warn">asks you first</Badge>}
              </li>
            ))}
          </ul>
        )}
        <p mix={faint} style={{ margin: '0' }}>
          Which tools only read is decided where the server is connected, in the Library. Every other tool waits for your approval, call by call.
        </p>

        <h4 mix={subhead}>Which agents of this project may use it</h4>
        {agents.length === 0 ? (
          <p mix={muted} style={{ margin: '0' }}>
            No agents on this project yet.
          </p>
        ) : null}
        <ul mix={agentList} aria-label={`Agents allowed to use ${srv.display}`}>
          {agents.map((a) => (
            <li key={a.id}>
              <label mix={grantRow}>
                <input
                  type="checkbox"
                  aria-label={`${a.name} may use ${srv.display}`}
                  checked={want.get(a.id) ?? granted(a.id)}
                  disabled={act.busy !== ''}
                  mix={on('change', async (e) => {
                    const c = e.currentTarget.checked;
                    want.set(a.id, c);
                    const ok = await act.run('access', () => s.client.mcpServerGrant(srv.projectId, srv.id, a.id, c));
                    if (!ok) want.delete(a.id);
                    onChanged();
                  })}
                />
                <Avatar id={a.id} name={a.name} size={18} kind="agent" />
                <span>{a.name}</span>
                {a.role ? <span mix={faint}>{a.role}</span> : null}
              </label>
            </li>
          ))}
        </ul>
        <p mix={faint} style={{ margin: '0' }}>
          No agent can use a server until you tick it here.
        </p>

        <div mix={actionsRow}>
          <Button
            size="sm"
            icon={Power}
            disabled={act.busy !== '' || ro.readOnly}
            title={ro.readOnly ? ro.reason : undefined}
            mix={[
              on('click', async () => {
                await act.run('toggle', () => s.client.mcpServerSetEnabled(srv.projectId, srv.id, !srv.enabled));
                onChanged();
              }),
            ]}
          >
            {srv.enabled ? 'Turn off in this project' : 'Turn on in this project'}
          </Button>
          {confirmRemove ? null : (
            <Button size="sm" variant="ghost" mix={[on('click', () => ((confirmRemove = true), void handle.update()))]}>
              Take out of this project
            </Button>
          )}
        </div>
        {confirmRemove ? (
          <div mix={confirmCss}>
            <span>Take {srv.display} out of this project? Its grants here go too; it stays in the Library.</span>
            <Button
              size="sm"
              variant="danger"
              disabled={act.busy !== ''}
              mix={[
                on('click', async () => {
                  const done = await act.run('remove', async () => {
                    await s.client.mcpServerRemove(srv.projectId, srv.id);
                    return true;
                  });
                  if (done) onChanged();
                }),
              ]}
            >
              Yes, take it out
            </Button>
            <Button size="sm" mix={[on('click', () => ((confirmRemove = false), void handle.update()))]}>
              Keep
            </Button>
          </div>
        ) : null}
      </article>
    );
  };
}

/**
 * The workspace's connected servers (the library: the built-in servers you connected and your own) that this project does not
 * have yet, each with its logo and Add to this project.
 */
export function AddFromLibrary(handle: Handle<{ projectId: string; have: readonly string[]; onAdded: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const lib = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });

  return () => {
    const { projectId, have, onAdded } = handle.props;
    const ro = s.workspace.license;
    const all = lib.data ?? [];
    const options = all.filter((x) => !have.includes(x.id));
    return (
      <Section
        label="Add a server from the library"
        title="Add from the library"
        description="The servers connected once for the whole workspace: the ones built into Drydock that you connected, and your own."
        testId="add-from-library"
      >
        <ErrorNote error={lib.error ?? act.error} what="Not added" />
        {lib.loading && !lib.data ? <Loading /> : null}
        {lib.data && options.length === 0 ? (
          <p mix={[card, muted]} style={{ margin: '0' }}>
            {all.length === 0 ? 'No servers are connected yet.' : 'Every connected server is already in this project.'} Connect servers once in the{' '}
            <a href={routes.library.href()}>Library</a> or on the <a href={routes.integrations.href()}>Integrations</a> page, then add them here.
          </p>
        ) : null}
        {options.length > 0 ? (
          <ul mix={listCard} aria-label="Connected servers this project does not have">
            {options.map((x) => (
              <li key={x.id} mix={libRow} data-testid="library-server" data-server={x.id}>
                <ServerMark s={x} size={28} />
                <span class="who">
                  <strong>{x.display}</strong>
                  <span mix={faint}>
                    <code>{x.id}</code> · {x.catalogId ? 'built into Drydock' : 'your own'} · {x.tools.length > 0 ? `${x.tools.length} tool${x.tools.length === 1 ? '' : 's'}` : 'tools not found yet'}
                  </span>
                </span>
                <Button
                  size="sm"
                  icon={FolderPlus}
                  disabled={act.busy !== '' || ro.readOnly}
                  title={ro.readOnly ? ro.reason : undefined}
                  aria-label={`Add ${x.display} to this project`}
                  mix={[
                    on('click', async () => {
                      const r = await act.run(`add-${x.id}`, () => s.client.mcpServerAdd(projectId, x.id));
                      if (r) onAdded();
                    }),
                  ]}
                >
                  {act.busy === `add-${x.id}` ? 'Adding...' : 'Add to this project'}
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
      </Section>
    );
  };
}

// MCP servers of one project: the tool calls waiting for the human's approval (Approvals) and the project's agent tools
// (ProjectServers): a compact row per connected server (decision P98), the ones the project has first. The chosen row opens its
// panel under the rows: which of the project's agents may use the server (ServerCard), or Add to this project for one it does
// not have yet (LibraryServerPanel). They sit on the project's own Integrations page (/project/integrations,
// screens/integrations/project-integrations-screen.tsx); servers are connected once for the workspace, on the workspace's
// Integrations page (a vendor's card, or Your servers). A server nobody was granted is never reached.

import { css, on, type Handle } from 'remix/component';
import { BadgeCheck, CirclePlus, FolderPlus, Globe, Plug, Power, ShieldAlert } from 'lucide';
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
import { card, Section } from '../../ui/layout.tsx';
import { emptyLine } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { projectServerState, serverBlurb, serverKey } from '../integrations/integrations.ts';
import { PickRows } from '../integrations/pick-rows.tsx';
import { ShownNotes, Targets } from '../shared/approval-text.tsx';
import { ReachNote } from './command-line.tsx';
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
// the selected tile's panel, right under the grid (the same card as the workspace page's panel)
const panelCss = css({ borderColor: t.borderStrong });

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
  // the note under the tools says which program a grant starts while the tools are not known (M24): the boxes are described by it
  const reachId = `reach-${handle.id}`;

  return () => {
    const { s: srv, agents, onChanged } = handle.props;
    const ro = s.workspace.license;
    const granted = (agentId: string) => srv.agents.some((a) => a.agentId === agentId);
    for (const [a, v] of want) if (granted(a) === v) want.delete(a); // the server agrees now
    // a grant lists the server's tools first when they were never listed, and for a program that means starting it
    const startsOnGrant = srv.tools.length === 0 && srv.discoveredAt === 0;
    return (
      <article mix={[card, cardBody, panelCss]} data-testid="mcp-server" data-server={srv.id}>
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
          <>
            <p mix={muted} style={{ margin: '0' }}>
              Tools are not known yet. Press Find tools on <a href={routes.integrations.href({ key: serverKey(srv.id) })}>its page in Integrations</a>.
            </p>
            {startsOnGrant ? <ReachNote server={srv} lead="Ticking an agent below, while the tools are not known," id={reachId} /> : null}
          </>
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
          Which tools only read is decided where the server is connected, on <a href={routes.integrations.href({ key: serverKey(srv.id) })}>its page in Integrations</a>. Every other tool waits for your approval, call by call.
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
                  aria-describedby={startsOnGrant ? reachId : undefined}
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
            <span>Take {srv.display} out of this project? Its grants here go too; it stays connected for the workspace.</span>
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

/** A connected server this project does not have yet, as the panel under its tile: what it is, and Add to this project. */
function LibraryServerPanel(handle: Handle<{ x: McpLibraryServer; projectId: string; onAdded: (id: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);

  return () => {
    const { x, projectId, onAdded } = handle.props;
    const ro = s.workspace.license;
    return (
      <article mix={[card, cardBody, panelCss]} data-testid="library-server" data-server={x.id}>
        <header mix={cardHead}>
          <ServerMark s={x} />
          <h3>{x.display}</h3>
          <code mix={faint}>{x.id}</code>
          <span mix={faint}>{serverBlurb(x)}</span>
        </header>
        <ErrorNote error={act.error} what="Not added" />
        <p mix={muted} style={{ margin: '0' }}>
          Connected once for the whole workspace. Add it to this project, then tick which of its agents may use it.
        </p>
        <div mix={actionsRow}>
          <Button
            size="sm"
            icon={FolderPlus}
            disabled={act.busy !== '' || ro.readOnly}
            title={ro.readOnly ? ro.reason : undefined}
            aria-label={`Add ${x.display} to this project`}
            mix={[
              on('click', async () => {
                const r = await act.run(`add-${x.id}`, () => s.client.mcpServerAdd(projectId, x.id));
                if (r) onAdded(x.id);
              }),
            ]}
          >
            {act.busy === `add-${x.id}` ? 'Adding...' : 'Add to this project'}
          </Button>
        </div>
      </article>
    );
  };
}

/**
 * The project's agent tools, laid out like the workspace's Integrations tiles: one tile per connected server (the library: the
 * built-in servers you connected and your own), the ones in this project first with who may use them, then the rest as "not
 * added". The selected tile's panel opens under the grid.
 */
export function ProjectServers(handle: Handle<{ projectId: string; agents: readonly Agent[] }>) {
  const s = services(handle);
  const servers = query(handle, {
    load: () => s.client.mcpServerList(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type.startsWith('mcp.server') || e.type === 'mcp.access_changed' || e.type.startsWith('agent.'),
  });
  const lib = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });
  // the server whose panel is open: undefined = the first in this project (else the first of the library), '' = none
  let selected: string | undefined;

  return () => {
    const { projectId, agents } = handle.props;
    const inProject = servers.data ?? [];
    const rest = (lib.data ?? []).filter((x) => !inProject.some((p) => p.id === x.id));
    const all: McpLibraryServer[] = [...inProject, ...rest];
    const current = selected === undefined ? all[0] : all.find((x) => x.id === selected);
    const mine = current && inProject.find((p) => p.id === current.id);
    return (
      <Section
        label="Agent tools"
        testId="project-mcp"
        title="Agent tools"
        description="The MCP servers connected for the workspace, and which of this project's agents may use each. A server nobody was granted is never reached; every tool not marked read-only waits for your approval."
      >
        <ErrorNote error={servers.error ?? lib.error} what="Could not load the servers" />
        {(servers.loading && !servers.data) || (lib.loading && !lib.data) ? <Loading /> : null}
        {servers.data && lib.data && inProject.length === 0 ? (
          <p mix={emptyLine} data-testid="no-project-servers">
            <Icon icon={CirclePlus} size={14} />
            <strong>No MCP servers in this project yet.</strong>{' '}
            {rest.length > 0 ? (
              'Choose a connected server to add it.'
            ) : (
              <>
                No servers are connected yet. Connect servers once on the <a href={routes.integrations.href()}>Integrations</a> page, then add them here.
              </>
            )}
          </p>
        ) : null}
        {all.length > 0 ? (
          <PickRows
            label="MCP servers"
            selected={current?.id}
            onPick={(id) => ((selected = current?.id === id ? '' : id), void handle.update())}
            rows={all.map((x) => {
              const p = inProject.find((y) => y.id === x.id);
              const st = projectServerState(p, agents);
              return {
                key: x.id,
                title: x.display,
                logo: serverLogo(x) || undefined,
                icon: x.transport === 'http' ? Globe : Plug,
                sub: `${serverBlurb(x)} · ${st.text}`,
                status: st.badge,
                testId: 'project-server-tile',
                data: { server: x.id, added: String(!!p) },
              };
            })}
          />
        ) : null}
        {mine ? (
          <ServerCard key={mine.id} s={mine} agents={agents} onChanged={servers.reload} />
        ) : current ? (
          <LibraryServerPanel
            key={current.id}
            x={current}
            projectId={projectId}
            onAdded={(id) => {
              selected = id; // the added server opens, to tick who may use it
              servers.reload();
            }}
          />
        ) : null}
      </Section>
    );
  };
}

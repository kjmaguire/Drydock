// MCP servers of one project: which connected servers the project has, which of its agents may use each one, and the tool
// calls waiting for the human's approval. Servers are connected once in the Library; this page puts them into the project
// and grants them. A server nobody was granted is never reached.

import { css, on, type Handle } from 'remix/component';
import { BadgeCheck, CirclePlus, FolderPlus, Power, Server, ShieldAlert, Wrench } from 'lucide';
import type { McpApproval, McpServer } from '../../api/mcpClient.ts';
import type { Agent } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge, faint, mono, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section, stack } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { ConnectorGallery } from './gallery.tsx';
import { whereOf } from './server-input.ts';

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
const addRow = css({ display: 'flex', alignItems: 'flex-end', gap: t.s2, flexWrap: 'wrap', '& > div': { minWidth: '260px' } });
const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd, maxWidth: '78ch' });

function Approvals(handle: Handle<{ projectId: string; agents: readonly Agent[] }>) {
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
                <code>{a.server}</code> / <code>{a.tool}</code>
              </h3>
              <span mix={faint}>
                {name(a.agentId)}, asked {formatDateTime(a.created)}
              </span>
            </header>
            <p mix={[muted]} style={{ margin: '0' }}>
              Arguments the agent sent (text from an agent, shown as is):
            </p>
            <pre mix={argsCss} tabIndex={0}>
              {a.args}
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

function ServerCard(handle: Handle<{ s: McpServer; agents: readonly Agent[]; onChanged: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let confirmRemove = false;

  return () => {
    const { s: srv, agents, onChanged } = handle.props;
    const ro = s.workspace.license;
    const granted = (agentId: string) => srv.agents.some((a) => a.agentId === agentId);
    return (
      <article mix={[card, cardBody]} data-testid="mcp-server" data-server={srv.id}>
        <header mix={cardHead}>
          <span mix={tile}>
            <Icon icon={Server} size={16} />
          </span>
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
            Tools are not known yet. Open the <a href={routes.library.href()}>Library</a> and press Find tools.
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
                  checked={granted(a.id)}
                  disabled={act.busy !== ''}
                  mix={on('change', async (e) => {
                    const c = e.currentTarget.checked;
                    await act.run('access', () => s.client.mcpServerGrant(srv.projectId, srv.id, a.id, c));
                    onChanged();
                  })}
                />
                <Avatar id={a.id} name={a.name} size={18} />
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

function AddFromLibrary(handle: Handle<{ projectId: string; have: string[]; onAdded: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const lib = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });
  let pick = '';

  return () => {
    const { projectId, have, onAdded } = handle.props;
    const all = lib.data ?? [];
    const options = all.filter((x) => !have.includes(x.id));
    const chosen = options.some((o) => o.id === pick) ? pick : (options[0]?.id ?? '');
    return (
      <Section label="Add a server from the library" title="Add from the library">
        <ErrorNote error={lib.error ?? act.error} what="Not added" />
        {options.length === 0 ? (
          <p mix={[card, muted]} style={{ margin: '0' }}>
            {all.length === 0 ? 'No servers are connected yet.' : 'Every connected server is already in this project.'} Connect servers once in the{' '}
            <a href={routes.library.href()}>Library</a>, then put them into projects here or by dragging them there.
          </p>
        ) : (
          <div mix={[card, addRow]}>
            <Field label="Server">
              {(id) => (
                <select id={id} aria-label="Server to add" value={chosen} mix={on('change', (e) => ((pick = e.currentTarget.value), void handle.update()))}>
                  {options.map((x) => (
                    <option key={x.id} value={x.id} selected={x.id === chosen}>
                      {x.display} ({x.id})
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Button
              variant="primary"
              icon={FolderPlus}
              disabled={act.busy !== ''}
              mix={[
                on('click', async () => {
                  const r = await act.run('add', () => s.client.mcpServerAdd(projectId, chosen));
                  pick = '';
                  if (r) onAdded();
                }),
              ]}
            >
              Put into this project
            </Button>
          </div>
        )}
      </Section>
    );
  };
}

export function McpScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const q = query(handle, {
    load: () => s.client.mcpServerList(s.workspace.projectId!),
    key: () => s.workspace.projectId,
    enabled: () => !!s.workspace.projectId,
    when: (e) => e.type.startsWith('mcp.server') || e.type === 'mcp.access_changed' || e.type.startsWith('agent.'),
  });

  return () => {
    const projectId = s.workspace.projectId;
    const project = s.workspace.project;
    const intro = (
      <p mix={introCss}>
        An MCP server gives agents extra tools: documentation search, an issue tracker, a database. Connect a server once, here or in the <a href={routes.library.href()}>Library</a>, put
        it into the projects that should have it, and choose which agents of each project may use it. A server nobody was granted is never reached. Anything a server returns is
        treated as outside text, and every tool you have not marked read-only waits for your approval, call by call. Secrets stay in the Windows credential store. Claude Code
        agents receive these servers; another agent CLI receives them only after you allow it under Agent CLIs in the <a href={routes.library.href()}>Library</a>, because no real run has shown yet that it uses them.
      </p>
    );
    const libraryLink = (
      <a href={routes.library.href()} mix={buttonStyle('ghost', 'sm')}>
        <Icon icon={Wrench} size={14} />
        Connect servers in the Library
      </a>
    );
    if (!projectId) {
      return (
        <Page title="MCP servers" icon={Server} actions={libraryLink}>
          {intro}
          <ConnectorGallery />
          {s.workspace.projects.data ? (
            <Section title="In a project">
              <div mix={card}>
                <Empty title="No project selected" icon={Server}>
                  <p>MCP servers are put into a project and granted to its agents. Connect them above now; create or choose a project to add them to.</p>
                  <a href={routes.projectNew.href()}>Create a project</a>
                </Empty>
              </div>
            </Section>
          ) : (
            <Loading />
          )}
        </Page>
      );
    }
    const servers = q.data ?? [];
    const agents = s.project.agentList;
    return (
      <Page
        title="MCP servers"
        icon={Server}
        subtitle={project ? `${project.name}${q.data ? ` · ${servers.length} server${servers.length === 1 ? '' : 's'}` : ''}` : undefined}
        actions={libraryLink}
      >
        {intro}
        <ErrorNote error={q.error} what="Could not load the servers" />
        <Approvals projectId={projectId} agents={agents} />
        <ConnectorGallery />
        <Section title="In this project">
          {q.loading && !q.data ? <Loading /> : null}
          {q.data && servers.length === 0 ? (
            <div mix={card}>
              <Empty title="No MCP servers in this project yet." icon={CirclePlus}>
                <p>Put a connected server into this project below, or drag one onto it in the Library.</p>
              </Empty>
            </div>
          ) : null}
          <div mix={stack}>
            {servers.map((srv) => (
              <ServerCard key={srv.id} s={srv} agents={agents} onChanged={q.reload} />
            ))}
          </div>
        </Section>
        <AddFromLibrary projectId={projectId} have={servers.map((x) => x.id)} onAdded={q.reload} />
      </Page>
    );
  };
}

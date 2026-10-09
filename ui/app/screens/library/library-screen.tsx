// The Library: MCP servers and team members (agent templates) defined once for the whole workspace, then dropped into projects
// and onto agents. Everything that can be dragged also has a "Put in project" or "Give" control, so the page works from the
// keyboard. A project only ever reaches the servers it was given, and an agent only the servers you gave it.

import { css, on, type Handle } from 'remix/component';
import {
  FolderOpen,
  FolderPlus,
  Globe,
  GripVertical,
  KeyRound,
  Library,
  Pencil,
  Plug,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  Unplug,
  UserPlus,
  Users,
  X,
} from 'lucide';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import { AgentGallery } from '../team/agent-gallery.tsx';
import type { McpLibraryServer, McpServer } from '../../api/mcpClient.ts';
import type { AdapterInfo, Agent, Project } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar, Badge, faint, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section, stack } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { CliGateway } from '../mcp/cli-gateway.tsx';
import { actionsRow, cardHead, confirmCss, subhead, tile, whereCss } from '../mcp/mcp-screen.tsx';
import { OAuthPanel } from '../mcp/oauth-panel.tsx';
import { McpServerForm } from '../mcp/server-form.tsx';
import { inputOf, whereOf } from '../mcp/server-input.ts';
import { TemplateForm } from '../team/template-form.tsx';
import { joinedNote, startDrag, type Dragged } from './dnd.ts';
import { DropZone } from './drop-zone.tsx';

type Say = (message: string) => void;
type Fail = (e: Error | undefined) => void;

const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd, maxWidth: '86ch' });
const colsCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1.45fr) minmax(300px, 1fr)',
  gap: t.s6,
  alignItems: 'start',
  '@media (max-width: 980px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
});
const sideCss = css({ position: 'sticky', top: t.s4, display: 'grid', gap: t.s3 });
const noteCss = css({ '&:empty': { display: 'none' } });

const dragCard = css({
  display: 'grid',
  gap: t.s3,
  transition: `box-shadow ${t.fast} ${t.ease}, border-color ${t.fast} ${t.ease}, opacity ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong, boxShadow: t.shadowSm },
  '&[data-dragging="true"]': { opacity: '0.55', borderStyle: 'dashed' },
  '& [data-grip]': { color: t.textFaint, cursor: 'grab', display: 'inline-flex' },
  '& p': { margin: '0' },
});
const dragHint = css({ fontSize: t.textSm, color: t.textFaint });
const metaRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', fontSize: t.textSm, color: t.textMuted, '& svg': { color: t.textFaint } });
const toolsTable = css({
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: t.textMd,
  '& th': { textAlign: 'left', fontSize: t.textXs, fontWeight: '600', color: t.textMuted, padding: '6px 10px', borderBottom: `1px solid ${t.border}` },
  '& td': { padding: '8px 10px', borderBottom: `1px solid ${t.border}`, verticalAlign: 'top' },
  '& tr:last-child td': { borderBottom: '0' },
  '& td > div': { color: t.textMuted, fontSize: t.textSm, marginTop: '2px' },
  '& label': { display: 'inline-flex', alignItems: 'center', gap: t.s2, cursor: 'pointer' },
});
const tableWrap = css({ border: `1px solid ${t.border}`, borderRadius: t.rMd, overflow: 'hidden' });
const chips = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '6px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '4px',
    height: '24px',
    padding: '0 4px 0 8px',
    borderRadius: t.rFull,
    border: `1px solid ${t.border}`,
    background: t.bgApp,
    fontSize: t.textSm,
  },
  '& > li[data-plain]': { paddingRight: '8px' },
  '& > li button': { width: '18px', height: '18px', borderRadius: '50%' },
});
const putRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', '& select': { width: 'auto', minWidth: '140px', maxWidth: '100%' } });
const panelCss = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
const agentRow = css({
  display: 'grid',
  gap: '6px',
  padding: `${t.s2} ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& .who': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', minWidth: '0' },
  '& .who strong': { fontWeight: '600' },
});

/** Put an item into a project from a select, for people who do not drag. */
function PutInProject(handle: Handle<{ projects: readonly Project[]; label: string; onPick: (projectId: string) => void; busy: boolean }>) {
  let id = '';
  return () => {
    const { projects, label, onPick, busy } = handle.props;
    if (projects.length === 0) return <p mix={faint}>Create a project first.</p>;
    const chosen = projects.some((p) => p.id === id) ? id : projects[0]!.id;
    return (
      <div mix={putRow}>
        <select aria-label={label} value={chosen} mix={on('change', (e) => ((id = e.currentTarget.value), void handle.update()))}>
          {projects.map((p) => (
            <option key={p.id} value={p.id} selected={p.id === chosen}>
              {p.name}
            </option>
          ))}
        </select>
        <Button size="sm" icon={FolderPlus} disabled={busy} mix={[on('click', () => onPick(chosen))]}>
          Put in project
        </Button>
      </div>
    );
  };
}

function McpCard(handle: Handle<{ s: McpLibraryServer; projects: readonly Project[]; onDragging: (d: Dragged | undefined) => void; say: Say; fail: Fail; onChanged: () => void }>) {
  const svc = services(handle);
  watch(handle, svc.workspace);
  const act = action(handle);
  let editing = false;
  let confirm = false;
  let dragging = false;
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
    const { s, projects, say, fail, onChanged } = handle.props;
    const ro = svc.workspace.license;
    const inProjects = s.projects.map((id) => projects.find((p) => p.id === id)?.name ?? id);
    return (
      <>
        <article
          mix={[
            card,
            dragCard,
            on<HTMLElement, 'dragstart'>('dragstart', (e) => {
              const item: Dragged = { kind: 'mcp', id: handle.props.s.id };
              startDrag(e, item);
              set(() => (dragging = true));
              handle.props.onDragging(item);
            }),
            on<HTMLElement, 'dragend'>('dragend', () => {
              set(() => (dragging = false));
              handle.props.onDragging(undefined);
            }),
          ]}
          draggable="true"
          data-dragging={String(dragging)}
          data-testid="lib-mcp"
          data-server={s.id}
        >
          <header mix={cardHead}>
            <span data-grip aria-hidden="true">
              <Icon icon={GripVertical} size={16} />
            </span>
            <span mix={tile}>
              <Icon icon={s.transport === 'http' ? Globe : Plug} size={16} />
            </span>
            <h3>{s.display}</h3>
            <code mix={faint}>{s.id}</code>
            <Badge>{s.transport === 'http' ? 'web address' : 'program'}</Badge>
            {s.guard ? (
              <Badge tone="info" title="Agents cannot send code, files, secrets or long text to it">
                send guard on
              </Badge>
            ) : (
              <Badge tone="warn">send guard off</Badge>
            )}
          </header>
          <p mix={dragHint}>Drag onto a project to add it there, or onto an agent to let that agent use it.</p>
          <ErrorNote error={act.error} what="Not changed" />
          <code mix={[mono, whereCss]}>{whereOf(s)}</code>
          {s.oauth ? <OAuthPanel server={s} onChanged={onChanged} /> : null}
          {s.secretEnv.length > 0 || s.hasToken ? (
            <p mix={metaRow} data-testid="mcp-secrets">
              <Icon icon={KeyRound} size={14} />
              Secrets kept in the credential store: {s.hasToken ? 'bearer token' : s.secretEnv.join(', ')} (values are never shown)
            </p>
          ) : null}
          <p mix={metaRow} data-testid="lib-mcp-projects">
            <Icon icon={FolderOpen} size={14} />
            {inProjects.length === 0 ? 'Not in any project yet.' : `In: ${inProjects.join(', ')}.`}
          </p>
          <h4 mix={subhead}>Tools</h4>
          {s.tools.length === 0 ? (
            <p mix={muted}>Tools are not known yet. Press Find tools to start the server and list them.</p>
          ) : (
            <div mix={tableWrap}>
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
                        {tool.description ? <div>{tool.description}</div> : null}
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
            </div>
          )}
          <p mix={faint}>Every tool you do not tick needs your approval for each call. Ticking is your statement that the tool cannot change anything.</p>
          <PutInProject
            projects={projects}
            label={`Put ${s.display} in project`}
            busy={act.busy !== ''}
            onPick={async (pid) => {
              const r = await act.run('add', () => svc.client.mcpServerAdd(pid, s.id));
              if (r) {
                say(`${s.display} is now in ${projects.find((p) => p.id === pid)?.name ?? pid}.`);
                onChanged();
              }
            }}
          />
          <div mix={actionsRow}>
            <Button
              size="sm"
              icon={s.tools.length > 0 ? RefreshCw : Search}
              disabled={act.busy !== ''}
              mix={[
                on('click', async () => {
                  const r = await act.run('discover', () => svc.client.mcpLibDiscover(s.id));
                  if (r) onChanged();
                }),
              ]}
            >
              {act.busy === 'discover' ? 'Connecting...' : s.tools.length > 0 ? 'Test and refresh tools' : 'Find tools'}
            </Button>
            <Button size="sm" variant="ghost" icon={Pencil} aria-expanded={editing} mix={[on('click', () => set(() => (editing = !editing)))]}>
              Edit
            </Button>
            {confirm ? null : (
              <Button size="sm" variant="ghost" icon={Unplug} mix={[on('click', () => set(() => (confirm = true)))]}>
                Disconnect
              </Button>
            )}
          </div>
          {confirm ? (
            <div mix={confirmCss}>
              <span>
                Disconnect {s.display}? It leaves {s.projects.length} project(s) and its stored secrets are deleted.
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
                    if (done) {
                      fail(undefined);
                      say(`${s.display} was disconnected.`);
                      onChanged();
                    }
                  }),
                ]}
              >
                Yes, disconnect
              </Button>
              <Button size="sm" mix={[on('click', () => set(() => (confirm = false)))]}>
                Keep
              </Button>
            </div>
          ) : null}
        </article>
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
      </>
    );
  };
}

function TemplateCard(
  handle: Handle<{
    t: AgentTemplate;
    servers: readonly McpLibraryServer[];
    projects: readonly Project[];
    adapters: readonly AdapterInfo[];
    onDragging: (d: Dragged | undefined) => void;
    say: Say;
    onChanged: () => void;
  }>,
) {
  const svc = services(handle);
  const act = action(handle);
  let editing = false;
  let confirm = false;
  let dragging = false;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };

  return () => {
    const { t: tp, servers, projects, adapters, say, onChanged } = handle.props;
    const nameOf = (id: string) => servers.find((x) => x.id === id)?.display ?? id;
    return (
      <>
        <article
          mix={[
            card,
            dragCard,
            on<HTMLElement, 'dragstart'>('dragstart', (e) => {
              const item: Dragged = { kind: 'template', id: handle.props.t.id };
              startDrag(e, item);
              set(() => (dragging = true));
              handle.props.onDragging(item);
            }),
            on<HTMLElement, 'dragend'>('dragend', () => {
              set(() => (dragging = false));
              handle.props.onDragging(undefined);
            }),
          ]}
          draggable="true"
          data-dragging={String(dragging)}
          data-testid="lib-template"
          data-template={tp.name}
        >
          <header mix={cardHead}>
            <span data-grip aria-hidden="true">
              <Icon icon={GripVertical} size={16} />
            </span>
            <Avatar id={tp.id} name={tp.name} size={30} />
            <h3>{tp.name}</h3>
            {tp.role ? <span mix={muted}>{tp.role}</span> : null}
            <code mix={faint}>{tp.adapter}</code>
            {tp.model ? <Badge>model {tp.model}</Badge> : null}
            {tp.effort ? <Badge>effort {tp.effort}</Badge> : null}
          </header>
          <p mix={dragHint}>Drag onto a project to add this team member there.</p>
          <ErrorNote error={act.error} what="Not changed" />
          {tp.purpose ? <p>{tp.purpose.length > 200 ? `${tp.purpose.slice(0, 200)}...` : tp.purpose}</p> : <p mix={muted}>No purpose written.</p>}
          {tp.mcpServers.length > 0 ? (
            <ul mix={chips} aria-label="MCP servers it gets">
              {tp.mcpServers.map((id) => (
                <li key={id} data-plain>
                  <Icon icon={Plug} size={12} />
                  {nameOf(id)}
                </li>
              ))}
            </ul>
          ) : null}
          <PutInProject
            projects={projects}
            label={`Put ${tp.name} in project`}
            busy={act.busy !== ''}
            onPick={async (pid) => {
              const r = await act.run('apply', () => svc.client.agentTemplateApply(tp.id, pid));
              if (r) {
                const pname = projects.find((p) => p.id === pid)?.name ?? pid;
                say(joinedNote(r.agent.name, pname, r.granted, r.skipped, nameOf));
                onChanged();
              }
            }}
          />
          <div mix={actionsRow}>
            <Button size="sm" variant="ghost" icon={Pencil} aria-expanded={editing} mix={[on('click', () => set(() => (editing = !editing)))]}>
              Edit
            </Button>
            {confirm ? null : (
              <Button size="sm" variant="ghost" icon={Trash2} mix={[on('click', () => set(() => (confirm = true)))]}>
                Delete
              </Button>
            )}
          </div>
          {confirm ? (
            <div mix={confirmCss}>
              <span>Delete the template {tp.name}? Agents already made from it stay.</span>
              <Button
                size="sm"
                variant="danger"
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    const done = await act.run('delete', async () => {
                      await svc.client.agentTemplateDelete(tp.id);
                      return true;
                    });
                    if (done) onChanged();
                  }),
                ]}
              >
                Yes, delete
              </Button>
              <Button size="sm" mix={[on('click', () => set(() => (confirm = false)))]}>
                Keep
              </Button>
            </div>
          ) : null}
        </article>
        <Dialog open={editing} title={`Edit the template ${tp.name}`} wide onClose={() => set(() => (editing = false))}>
          {editing ? (
            <TemplateForm
              initial={tp}
              adapters={adapters}
              servers={servers}
              onCancel={() => set(() => (editing = false))}
              onDone={() => {
                set(() => (editing = false));
                onChanged();
              }}
            />
          ) : null}
        </Dialog>
      </>
    );
  };
}

function GiveServer(handle: Handle<{ agent: Agent; options: readonly McpLibraryServer[]; busy: boolean; onGive: (serverId: string) => void }>) {
  let id = '';
  return () => {
    const { agent, options, busy, onGive } = handle.props;
    const chosen = options.some((o) => o.id === id) ? id : options[0]!.id;
    return (
      <span mix={putRow}>
        <select aria-label={`Server to give ${agent.name}`} value={chosen} mix={on('change', (e) => ((id = e.currentTarget.value), void handle.update()))}>
          {options.map((x) => (
            <option key={x.id} value={x.id} selected={x.id === chosen}>
              {x.display}
            </option>
          ))}
        </select>
        <Button size="sm" variant="ghost" icon={UserPlus} disabled={busy} mix={[on('click', () => onGive(chosen))]}>
          Give to {agent.name}
        </Button>
      </span>
    );
  };
}

function ProjectPanel(handle: Handle<{ project: Project; servers: readonly McpLibraryServer[]; dragging: Dragged | undefined; say: Say; fail: Fail }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const mine = query(handle, {
    load: () => s.client.mcpServerList(handle.props.project.id),
    key: () => handle.props.project.id,
    when: (e) => e.type.startsWith('mcp.') || e.type.startsWith('agenttemplate.'),
  });
  const team = query(handle, {
    load: () => s.client.agentList(handle.props.project.id),
    key: () => handle.props.project.id,
    when: (e) => e.type.startsWith('agent.') || e.type.startsWith('agenttemplate.'),
  });
  const reload = () => {
    mine.reload();
    team.reload();
  };
  const serverName = (id: string) => handle.props.servers.find((x) => x.id === id)?.display ?? id;

  const dropOnProject = async (item: Dragged) => {
    const { project, say, fail } = handle.props;
    fail(undefined);
    if (item.kind === 'mcp') {
      const r = await act.run('add', () => s.client.mcpServerAdd(project.id, item.id));
      if (r) say(`${serverName(item.id)} is now in ${project.name}.`);
    } else {
      const r = await act.run('apply', () => s.client.agentTemplateApply(item.id, project.id));
      if (r) say(joinedNote(r.agent.name, project.name, r.granted, r.skipped, serverName));
    }
    reload();
  };
  const grant = async (agent: Agent, serverId: string, enabled: boolean) => {
    const { say, fail } = handle.props;
    fail(undefined);
    const r = await act.run('grant', () => s.client.mcpServerGrant(handle.props.project.id, serverId, agent.id, enabled));
    if (r) say(enabled ? `${agent.name} can now use ${serverName(serverId)}.` : `${agent.name} can no longer use ${serverName(serverId)}.`);
    reload();
  };

  return () => {
    const { project, servers, dragging, say } = handle.props;
    const ro = s.workspace.license;
    const inProject: McpServer[] = mine.data ?? [];
    const agents: Agent[] = team.data ?? [];
    const off = act.busy !== '' || ro.readOnly;
    return (
      <DropZone
        accepts={['mcp', 'template']}
        dragging={dragging}
        onDropItem={(item) => void dropOnProject(item)}
        label={`Project ${project.name}: drop an MCP server or an agent template here`}
        testId="lib-project"
      >
        <section mix={[card, panelCss]} data-project={project.name} aria-label={`Project ${project.name}`}>
          <header mix={cardHead}>
            <Avatar id={project.id} name={project.name} size={28} square />
            <h3>{project.name}</h3>
            {dragging ? <Badge tone="info">drop here</Badge> : null}
          </header>
          <ErrorNote error={act.error ?? mine.error ?? team.error} what="Not done" />
          <h4 mix={subhead}>MCP servers in this project</h4>
          {inProject.length === 0 ? (
            <p mix={faint}>None. Drop a server here.</p>
          ) : (
            <ul mix={chips} aria-label={`MCP servers in ${project.name}`}>
              {inProject.map((x) => (
                <li key={x.id} data-testid="proj-server" data-server={x.id}>
                  <Icon icon={Plug} size={12} />
                  {x.display}
                  <IconButton
                    icon={X}
                    size={12}
                    label={`Take ${x.display} out of ${project.name}`}
                    disabled={off}
                    mix={[
                      on('click', async () => {
                        await act.run('remove', () => s.client.mcpServerRemove(project.id, x.id));
                        say(`${x.display} was taken out of ${project.name}.`);
                        reload();
                      }),
                    ]}
                  />
                </li>
              ))}
            </ul>
          )}
          <h4 mix={subhead}>Team</h4>
          {agents.length === 0 ? <p mix={faint}>No agents. Drop a template here to add one.</p> : null}
          {agents.map((a) => {
            const mineIds = inProject.filter((x) => x.agents.some((g) => g.agentId === a.id));
            const options = servers.filter((x) => !mineIds.some((m) => m.id === x.id));
            return (
              <DropZone
                key={a.id}
                inner
                accepts={['mcp']}
                dragging={dragging}
                onDropItem={(item) => void grant(a, item.id, true)}
                label={`Agent ${a.name}: drop an MCP server here to let it use the server`}
                testId="lib-agent"
              >
                <div mix={agentRow} data-agent={a.name}>
                  <div class="who">
                    <Avatar id={a.id} name={a.name} size={20} />
                    <strong>{a.name}</strong>
                    {a.role ? <span mix={muted}>{a.role}</span> : null}
                    <code mix={faint}>{a.adapter}</code>
                  </div>
                  {mineIds.length > 0 ? (
                    <ul mix={chips} aria-label={`Servers ${a.name} may use`}>
                      {mineIds.map((x) => (
                        <li key={x.id} data-testid="agent-server" data-server={x.id}>
                          {x.display}
                          <IconButton icon={X} size={12} label={`Stop ${a.name} using ${x.display}`} disabled={off} mix={[on('click', () => void grant(a, x.id, false))]} />
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <span mix={faint}>no MCP servers</span>
                  )}
                  {options.length > 0 ? <GiveServer agent={a} options={options} busy={off} onGive={(sid) => void grant(a, sid, true)} /> : null}
                </div>
              </DropZone>
            );
          })}
        </section>
      </DropZone>
    );
  };
}

export function LibraryScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const adapters = query(handle, { load: () => s.client.adapterList() });
  const servers = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });
  const templates = query(handle, { load: () => s.client.agentTemplateList(), when: (e) => e.type.startsWith('agenttemplate.') });
  const prebuilt = query(handle, { load: () => s.client.agentCatalogList() });
  let dragging: Dragged | undefined;
  let note = '';
  let error: Error | undefined;
  let connecting = false;
  let newTemplate = false;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const say: Say = (m) => set(() => (note = m));
  const fail: Fail = (e) => set(() => (error = e));
  const onDragging = (d: Dragged | undefined) => set(() => (dragging = d));
  const reload = () => {
    servers.reload();
    templates.reload();
  };

  return () => {
    const projects = s.workspace.projectList;
    const libServers = servers.data ?? [];
    const tpls = templates.data ?? [];
    const adapterList = adapters.data ?? [];
    return (
      <Page
        title="Library"
        icon={Library}
        wide
        subtitle={servers.data && templates.data ? `${libServers.length} server${libServers.length === 1 ? '' : 's'} · ${tpls.length} template${tpls.length === 1 ? '' : 's'}` : undefined}
      >
        <p mix={introCss}>
          Connect an MCP server or define a team member once, here. Then drag it onto a project to add it there, or drag a server onto an agent to let that agent use it. A project
          only ever reaches the servers it was given, and an agent only the servers you gave it.
        </p>
        <div role="status" aria-live="polite" data-testid="library-note" mix={noteCss}>
          {note ? (
            <Notice tone="ok">
              {note}
            </Notice>
          ) : null}
        </div>
        <ErrorNote error={error ?? servers.error ?? templates.error} what="Library" />
        <div mix={colsCss}>
          <div mix={stack} style={{ gap: 'var(--space-6)' }}>
            <Section
              title="MCP servers"
              description="Connected once for the whole workspace. Secrets stay in the Windows credential store."
              actions={
                <Button variant="primary" size="sm" icon={Plus} mix={[on('click', () => set(() => (connecting = true)))]}>
                  Connect an MCP server
                </Button>
              }
            >
              {servers.loading && !servers.data ? <Loading /> : null}
              {servers.data && libServers.length === 0 ? (
                <div mix={card}>
                  <Empty title="No servers are connected yet." icon={Plug}>
                    <p>Connect a program on this computer or a web address once. Then press Find tools and drag it onto a project.</p>
                  </Empty>
                </div>
              ) : null}
              <div mix={stack}>
                {libServers.map((x) => (
                  <McpCard key={x.id} s={x} projects={projects} onDragging={onDragging} say={say} fail={fail} onChanged={reload} />
                ))}
              </div>
            </Section>

            <CliGateway />

            <Section
              title="Agent templates"
              description="A team member defined once. Applying it makes an ordinary agent in a project."
              actions={
                <Button variant="primary" size="sm" icon={Plus} mix={[on('click', () => set(() => (newTemplate = true)))]}>
                  New agent template
                </Button>
              }
            >
              {templates.data && tpls.length === 0 ? (
                <div mix={card}>
                  <Empty title="No templates yet." icon={Users}>
                    <p>
                      Make one here, or save an agent as a template on the <a href={routes.team.href()}>Team</a> page.
                    </p>
                  </Empty>
                </div>
              ) : null}
              <div mix={stack}>
                {tpls.map((x) => (
                  <TemplateCard key={x.id} t={x} servers={libServers} projects={projects} adapters={adapterList} onDragging={onDragging} say={say} onChanged={reload} />
                ))}
              </div>
            </Section>

            <Section
              title="Prebuilt agents"
              description={
                <>
                  Built into Drydock, with their skills and connectors pinned. Add them to a project on the <a href={routes.team.href()}>Team</a> page or when you create a project.
                </>
              }
              testId="library-prebuilt"
            >
              <ErrorNote error={prebuilt.error} what="Could not load the prebuilt agents" />
              {prebuilt.loading && !prebuilt.data ? <Loading /> : null}
              <AgentGallery entries={prebuilt.data ?? []} label="Prebuilt agents" />
            </Section>
          </div>
          <div mix={sideCss}>
            <Section title="Projects" description="Drop servers and templates here.">
              {s.workspace.projects.data && projects.length === 0 ? (
                <div mix={card}>
                  <Empty title="No projects yet." icon={FolderOpen}>
                    <a href={routes.projectNew.href()}>Create a project</a>
                  </Empty>
                </div>
              ) : null}
              <div mix={stack}>
                {projects.map((p) => (
                  <ProjectPanel key={p.id} project={p} servers={libServers} dragging={dragging} say={say} fail={fail} />
                ))}
              </div>
            </Section>
          </div>
        </div>

        <Dialog open={connecting} title="Connect an MCP server" wide onClose={() => set(() => (connecting = false))}>
          {connecting ? (
            <McpServerForm
              onCancel={() => set(() => (connecting = false))}
              onDone={(saved) => {
                set(() => {
                  connecting = false;
                  note = `${saved.display} is connected. Press Find tools, then drag it onto a project.`;
                });
                reload();
              }}
            />
          ) : null}
        </Dialog>
        <Dialog open={newTemplate} title="New agent template" wide onClose={() => set(() => (newTemplate = false))}>
          {newTemplate ? (
            <TemplateForm
              adapters={adapterList}
              servers={libServers}
              onCancel={() => set(() => (newTemplate = false))}
              onDone={(saved) => {
                set(() => {
                  newTemplate = false;
                  note = `The template ${saved.name} was saved.`;
                });
                reload();
              }}
            />
          ) : null}
        </Dialog>
      </Page>
    );
  };
}

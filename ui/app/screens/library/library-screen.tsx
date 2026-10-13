// The Library: MCP servers, team members (agent templates) and skills defined once for the whole workspace, then dropped into
// projects and onto agents. Everything that can be dragged also has a "Put in project" or "Give" control, so the page works from
// the keyboard. A project only ever reaches the servers it was given, and an agent only the servers you gave it. The skill
// library fills itself from every enabled registry (decision P48); adding a skill to a project is the person's acceptance.

import { css, on, type Handle } from 'remix/component';
import {
  BookOpen,
  Check,
  Cpu,
  FolderOpen,
  FolderPlus,
  GitBranch,
  Globe,
  GripVertical,
  KeyRound,
  Library,
  Pencil,
  Plug,
  Plus,
  RefreshCw,
  Search,
  Sparkles,
  Trash2,
  Unplug,
  UserPlus,
  Users,
  X,
} from 'lucide';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import { AgentGallery } from '../team/agent-gallery.tsx';
import type { LibrarySkill, SkillLibraryAddAll } from '../../api/extTypes.ts';
import type { McpLibraryServer, McpServer } from '../../api/mcpClient.ts';
import type { AdapterInfo, Agent, Project } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar, Badge, faint, kv, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { BrandLogo, brandKey } from '../../ui/brand-logo.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section, stack } from '../../ui/layout.tsx';
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { CliGateway } from '../mcp/cli-gateway.tsx';
import { actionsRow, cardHead, confirmCss, subhead, tile, whereCss } from '../mcp/mcp-screen.tsx';
import { OAuthPanel } from '../mcp/oauth-panel.tsx';
import { McpServerForm } from '../mcp/server-form.tsx';
import { inputOf, whereOf } from '../mcp/server-input.ts';
import { addAllText, addedText, libraryBlurb, libraryStateText, listOr, registryStatusText, skillBrand } from '../skills/skills.ts';
import { skillLines } from '../team/team.ts';
import { TemplateForm } from '../team/template-form.tsx';
import { joinedNote, startDrag, type Dragged } from './dnd.ts';
import { DropZone } from './drop-zone.tsx';
import { serverBlurb, serverState, templateBlurb, templateState } from './tiles.ts';

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

// The selected entry's panel below its grid. Its header can be dragged like the tile.
const dragCard = css({
  display: 'grid',
  gap: t.s3,
  transition: `border-color ${t.fast} ${t.ease}, opacity ${t.fast} ${t.ease}`,
  '&[data-dragging="true"]': { opacity: '0.55', borderStyle: 'dashed' },
  '& [data-grip]': { color: t.textFaint, cursor: 'grab', display: 'inline-flex' },
  '& p': { margin: '0' },
});
const dragHead = css({ cursor: 'grab' });
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
  '& > li[data-in-project="true"]': { color: t.success, borderColor: `color-mix(in srgb, ${t.success} 40%, ${t.border})` },
});
const skillPanelCss = css({
  display: 'grid',
  gap: t.s3,
  '& h3': { fontFamily: t.mono, fontSize: t.textMd, fontWeight: '600', overflowWrap: 'anywhere' },
  '& p': { margin: '0' },
  '& .reasons': { margin: '0', paddingLeft: '16px', fontSize: t.textSm, color: t.textMuted },
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

/** The brand a server shows, from the catalog entry it was connected from or its own id; '' when it has none. */
export function serverLogo(s: Pick<McpLibraryServer, 'id' | 'catalogId'>): string {
  return [s.catalogId, s.id].find((x): x is string => !!x && brandKey(x) !== '') ?? '';
}

/** The drag handlers of one Library item (a tile or a panel header): what is dragged, and a flag the element dims by. */
function dragOf(item: () => Dragged, onDragging: (d: Dragged | undefined) => void, flag: (v: boolean) => void) {
  return {
    start: (e: DragEvent) => {
      const it = item();
      startDrag(e, it);
      flag(true);
      onDragging(it);
    },
    end: () => {
      flag(false);
      onDragging(undefined);
    },
  };
}

function ServerTile(handle: Handle<{ s: McpLibraryServer; selected: boolean; onSelect: () => void; onDragging: (d: Dragged | undefined) => void }>) {
  let dragging = false;
  const drag = dragOf(
    () => ({ kind: 'mcp', id: handle.props.s.id }),
    (d) => handle.props.onDragging(d),
    (v) => ((dragging = v), void handle.update()),
  );
  return () => {
    const { s, selected, onSelect } = handle.props;
    const st = serverState(s);
    return (
      <Tile
        title={s.display}
        logo={serverLogo(s) || undefined}
        icon={s.transport === 'http' ? Globe : Plug}
        blurb={serverBlurb(s)}
        state={st.text}
        tone={st.tone}
        selected={selected}
        onSelect={onSelect}
        testId="lib-mcp-tile"
        data={{ server: s.id }}
        dragging={dragging}
        onDragStart={drag.start}
        onDragEnd={drag.end}
      />
    );
  };
}

function TemplateTile(
  handle: Handle<{ t: AgentTemplate; libraryIds: ReadonlySet<string>; selected: boolean; onSelect: () => void; onDragging: (d: Dragged | undefined) => void }>,
) {
  let dragging = false;
  const drag = dragOf(
    () => ({ kind: 'template', id: handle.props.t.id }),
    (d) => handle.props.onDragging(d),
    (v) => ((dragging = v), void handle.update()),
  );
  return () => {
    const { t: tp, libraryIds, selected, onSelect } = handle.props;
    const st = templateState(tp, libraryIds);
    return (
      <Tile
        title={tp.name}
        logo={tp.adapter}
        icon={Users}
        blurb={templateBlurb(tp)}
        state={st.text}
        tone={st.tone}
        selected={selected}
        onSelect={onSelect}
        testId="lib-template-tile"
        data={{ template: tp.name }}
        dragging={dragging}
        onDragStart={drag.start}
        onDragEnd={drag.end}
      />
    );
  };
}

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

/** The selected server's full panel: where it runs, its secrets, its tools, and what can be done with it. */
function McpPanel(handle: Handle<{ s: McpLibraryServer; projects: readonly Project[]; onDragging: (d: Dragged | undefined) => void; say: Say; fail: Fail; onChanged: () => void }>) {
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
  const drag = dragOf(
    () => ({ kind: 'mcp', id: handle.props.s.id }),
    (d) => handle.props.onDragging(d),
    (v) => set(() => (dragging = v)),
  );

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
        <article mix={[card, dragCard]} data-dragging={String(dragging)} data-testid="lib-mcp" data-server={s.id} aria-label={s.display}>
          <header
            mix={[cardHead, dragHead, on<HTMLElement, 'dragstart'>('dragstart', drag.start), on<HTMLElement, 'dragend'>('dragend', drag.end)]}
            draggable="true"
            data-testid="lib-mcp-handle"
          >
            <span data-grip aria-hidden="true">
              <Icon icon={GripVertical} size={16} />
            </span>
            {serverLogo(s) ? (
              <BrandLogo id={serverLogo(s)} size={32} />
            ) : (
              <span mix={tile}>
                <Icon icon={s.transport === 'http' ? Globe : Plug} size={16} />
              </span>
            )}
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
          <p mix={dragHint}>Drag its tile or this header onto a project to add it there, or onto an agent to let that agent use it.</p>
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
                        {tool.flags?.length ? (
                          <Badge tone="warn" title={`Instruction-like text found: ${tool.flags.join(', ')}`} testId="mcp-tool-flagged">
                            held back
                          </Badge>
                        ) : null}
                        {tool.flags?.length ? (
                          <div mix={faint}>
                            Its description reads like instructions to the agent ({tool.flags.join(', ')}). Drydock does not grant it to agents; the description is
                            shown here as data.
                          </div>
                        ) : null}
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

/** The selected template's full panel: its purpose, the servers and skills it gives, and what can be done with it. */
function TemplatePanel(
  handle: Handle<{
    t: AgentTemplate;
    servers: readonly McpLibraryServer[];
    projects: readonly Project[];
    adapters: readonly AdapterInfo[];
    /** The current project's skills by name, to mark the ones a template brings that the project already has. */
    inProject?: ReadonlySet<string>;
    projectName?: string;
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
  const drag = dragOf(
    () => ({ kind: 'template', id: handle.props.t.id }),
    (d) => handle.props.onDragging(d),
    (v) => set(() => (dragging = v)),
  );

  return () => {
    const { t: tp, servers, projects, adapters, say, onChanged, inProject, projectName } = handle.props;
    const nameOf = (id: string) => servers.find((x) => x.id === id)?.display ?? id;
    return (
      <>
        <article mix={[card, dragCard]} data-dragging={String(dragging)} data-testid="lib-template" data-template={tp.name} aria-label={tp.name}>
          <header
            mix={[cardHead, dragHead, on<HTMLElement, 'dragstart'>('dragstart', drag.start), on<HTMLElement, 'dragend'>('dragend', drag.end)]}
            draggable="true"
            data-testid="lib-template-handle"
          >
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
          <p mix={dragHint}>Drag its tile or this header onto a project to add this team member there.</p>
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
          {tp.skills.length > 0 ? (
            <ul mix={chips} aria-label="Skills it brings">
              {tp.skills.map((k) => {
                const has = inProject?.has(k.name) ?? false;
                return (
                  <li
                    key={k.name}
                    data-plain
                    data-in-project={String(has)}
                    title={`${k.registry}${k.ref ? ` at ${k.ref.slice(0, 12)}` : ''}${has ? ` · already in ${projectName ?? 'this project'}` : ''}`}
                  >
                    <Icon icon={has ? Check : Cpu} size={12} />
                    {k.name}
                    {has ? <span class="sr-only"> (in {projectName ?? 'this project'})</span> : null}
                  </li>
                );
              })}
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
                say(joinedNote(r.agent.name, pname, r.granted, r.skipped, nameOf, skillLines(r.skills)));
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

/**
 * The skill library: a tile per registry with its sync state; the selected registry's skills as tiles, each saying whether the
 * chosen project has it; Add to <project> (the acceptance) and Add all. The project starts as the current one.
 */
function SkillsLibrary(handle: Handle<{ projects: readonly Project[] }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const regs = query(handle, { load: () => s.client.skillRegistries() });
  const st = query(handle, { load: () => s.client.skillRegistryStatus(), when: (e) => e.type === 'skill.library_synced' || e.type === 'skill.registry_updates' });
  const lib = query(handle, { load: () => s.client.skillLibraryList(), when: (e) => e.type === 'skill.library_synced' || e.type === 'skill.registry_updates' });
  let project = '';
  const target = () => (handle.props.projects.some((p) => p.id === project) ? project : (s.workspace.projectId ?? handle.props.projects[0]?.id ?? ''));
  const mine = query(handle, {
    load: () => s.client.skillList(target()),
    key: target,
    enabled: () => target() !== '',
    when: (e) => e.type.startsWith('skill.'),
  });
  const act = action(handle);
  let registry = '';
  let selected = '';
  let added: { name: string; project: string } | undefined;
  let result: { repo: string; project: string; r: SkillLibraryAddAll } | undefined;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const projectName = (id: string) => handle.props.projects.find((p) => p.id === id)?.name ?? id;
  const add = async (l: LibrarySkill) => {
    const pid = target();
    const r = await act.run('add', () => s.client.skillLibraryAdd(pid, l.registryId, l.name));
    if (r) {
      set(() => {
        added = { name: l.name, project: projectName(pid) };
        result = undefined;
      });
      mine.reload();
    }
  };
  const addAll = async (registryId: string, repo: string) => {
    const pid = target();
    const r = await act.run('addAll', () => s.client.skillLibraryAddAll(pid, registryId));
    if (r) {
      set(() => {
        result = { repo, project: projectName(pid), r };
        added = undefined;
      });
      mine.reload();
      lib.reload();
    }
  };
  const sync = async (registryId?: string) => {
    await act.run('sync', () => s.client.skillRegistryCheck(registryId));
    st.reload();
    lib.reload();
  };
  return () => {
    const { projects } = handle.props;
    const pid = target();
    const pname = projectName(pid);
    const registries = regs.data ?? [];
    const statuses = st.data ?? [];
    const library = lib.data ?? [];
    const have = new Set((mine.data ?? []).map((k) => k.name));
    const openReg = registries.find((r) => r.id === registry);
    const skills = openReg ? library.filter((l) => l.registryId === openReg.id) : [];
    const openSkill = skills.find((l) => l.name === selected);
    const openStatus = openReg ? statuses.find((x) => x.registryId === openReg.id) : undefined;
    return (
      <Section
        title="Skills"
        description="Every skill of your registries, synced by Drydock at start and twice a day. Add one to a project: adding is your acceptance, and it reaches that project's agents at once."
        testId="library-skills"
      >
        <ErrorNote error={regs.error ?? st.error ?? lib.error ?? act.error} what="Skills" />
        {added ? (
          <Notice tone="ok" role="status" testId="skill-added">
            {addedText(added.name, added.project)}
          </Notice>
        ) : null}
        {result ? (
          <Notice tone={result.r.blocked.length + result.r.skipped.length > 0 ? 'warn' : 'ok'} role="status" testId="skill-added-all" title={`From ${result.repo} to ${result.project}:`}>
            {addAllText(result.r)}
            {result.r.blocked.length + result.r.skipped.length > 0 ? (
              <ul class="reasons" aria-label={`Skills not added from ${result.repo}`} style={{ margin: '4px 0 0', paddingLeft: '16px' }}>
                {result.r.blocked.map((b) => (
                  <li key={`b-${b.name}`}>
                    <code>{b.name}</code> blocked: {b.reason}
                  </li>
                ))}
                {result.r.skipped.map((k) => (
                  <li key={`s-${k.name}`}>
                    <code>{k.name}</code> skipped: {k.reason}
                  </li>
                ))}
              </ul>
            ) : null}
          </Notice>
        ) : null}
        {regs.loading && !regs.data ? <Loading /> : null}
        {regs.data && registries.length === 0 ? (
          <p mix={emptyLine} data-testid="lib-skills-empty">
            <Icon icon={BookOpen} size={14} />
            <strong>No skill registry is allowed yet.</strong> Allow one on the <a href={routes.skills.href()}>Skills</a> page.
          </p>
        ) : null}
        {registries.length > 0 ? (
          <ul mix={tileGrid} aria-label="Skill registries">
            {registries.map((r) => {
              const status = statuses.find((x) => x.registryId === r.id);
              const state = registryStatusText(status, r.enabled);
              const n = library.filter((l) => l.registryId === r.id).length;
              return (
                <Tile
                  key={r.id}
                  title={r.repo}
                  logo={skillBrand(r.id, 'registry') || undefined}
                  icon={GitBranch}
                  blurb={`${r.curated ? 'Curated' : 'Added by you'}${n > 0 ? ` · ${n} skill${n === 1 ? '' : 's'}` : ''}`}
                  state={state.text}
                  tone={state.tone}
                  selected={registry === r.id}
                  onSelect={() =>
                    set(() => {
                      registry = registry === r.id ? '' : r.id;
                      selected = '';
                    })
                  }
                  testId="lib-skill-registry-tile"
                  data={{ registry: r.id }}
                />
              );
            })}
          </ul>
        ) : null}
        {lib.data && registries.length > 0 && library.length === 0 ? (
          <p mix={emptyLine} data-testid="lib-skills-filling">
            <Icon icon={RefreshCw} size={14} />
            <strong>The Library is filling from your registries...</strong> It syncs at start and twice a day.
            <Button size="sm" icon={RefreshCw} disabled={act.busy !== ''} mix={[on('click', () => void sync())]}>
              {act.busy === 'sync' ? 'Syncing...' : 'Sync now'}
            </Button>
          </p>
        ) : null}
        {openReg ? (
          <section mix={[card, skillPanelCss]} data-testid="lib-skill-registry" data-registry={openReg.id} aria-label={openReg.repo}>
            <header mix={cardHead}>
              {skillBrand(openReg.id, 'registry') && brandKey(skillBrand(openReg.id, 'registry')) ? (
                <BrandLogo id={skillBrand(openReg.id, 'registry')} size={32} />
              ) : (
                <span mix={tile}>
                  <Icon icon={GitBranch} size={16} />
                </span>
              )}
              <h3>{openReg.repo}</h3>
              <Badge tone={openReg.enabled ? 'ok' : 'muted'}>{openReg.enabled ? 'enabled' : 'disabled'}</Badge>
              {openReg.curated ? <Badge>curated</Badge> : <Badge tone="warn">added by you</Badge>}
            </header>
            <p mix={metaRow} data-testid="lib-skill-registry-status">
              {registryStatusText(openStatus, openReg.enabled).text}
            </p>
            {projects.length === 0 ? (
              <p mix={faint}>Create a project first.</p>
            ) : (
              <div mix={putRow}>
                <label>
                  Add to project{' '}
                  <select aria-label="Project to add skills to" value={pid} mix={on('change', (e) => set(() => (project = e.currentTarget.value)))}>
                    {projects.map((p) => (
                      <option key={p.id} value={p.id} selected={p.id === pid}>
                        {p.name}
                      </option>
                    ))}
                  </select>
                </label>
                {skills.some((l) => l.status !== 'blocked' && !have.has(l.name)) ? (
                  <Button size="sm" icon={Plus} disabled={act.busy !== ''} data-testid="lib-skill-add-all" mix={[on('click', () => void addAll(openReg.id, openReg.repo))]}>
                    Add all to {pname}
                  </Button>
                ) : null}
                <Button size="sm" variant="ghost" icon={RefreshCw} disabled={!openReg.enabled || act.busy !== ''} mix={[on('click', () => void sync(openReg.id))]}>
                  {act.busy === 'sync' ? 'Syncing...' : 'Sync now'}
                </Button>
              </div>
            )}
            {skills.length === 0 ? (
              <p mix={faint}>No skill from this registry is in the Library yet.</p>
            ) : (
              <ul mix={tileGrid} aria-label={`Skills from ${openReg.repo}`}>
                {skills.map((l) => {
                  const has = have.has(l.name);
                  const state = has ? { text: `In ${pname} ✓`, tone: 'ok' as const } : libraryStateText(l);
                  return (
                    <Tile
                      key={l.id}
                      title={l.name}
                      logo={skillBrand(l.registryId, 'registry') || undefined}
                      icon={Sparkles}
                      blurb={libraryBlurb(l)}
                      state={state.text}
                      tone={state.tone}
                      selected={selected === l.name}
                      onSelect={() => set(() => (selected = selected === l.name ? '' : l.name))}
                      testId="lib-skill-tile"
                      data={{ name: l.name, registry: l.registryId, status: l.status, check: String(has) }}
                    />
                  );
                })}
              </ul>
            )}
            {openSkill ? (
              <article mix={[card, skillPanelCss]} data-testid="lib-skill" data-name={openSkill.name} aria-label={openSkill.name}>
                <header mix={cardHead}>
                  <h3>{openSkill.name}</h3>
                  {openSkill.license ? <Badge title="License">{openSkill.license}</Badge> : null}
                  {have.has(openSkill.name) ? (
                    <Badge tone="ok" testId="lib-skill-in-project">
                      In {pname} ✓
                    </Badge>
                  ) : null}
                  {openSkill.status === 'blocked' ? <Badge tone="err">blocked</Badge> : null}
                  {openSkill.risk > 0 ? <Badge tone="warn">risk {openSkill.risk}</Badge> : null}
                  {openSkill.hasScripts ? (
                    <Badge tone="warn" title="The skill directory contains scripts. Scripts are never run by Drydock.">
                      has scripts
                    </Badge>
                  ) : null}
                </header>
                {openSkill.description ? <p>{openSkill.description}</p> : <p mix={muted}>No description.</p>}
                <dl mix={kv}>
                  <dt>Roles it names</dt>
                  <dd>{listOr(openSkill.roles)}</dd>
                  <dt>Paths</dt>
                  <dd>{listOr(openSkill.globs)}</dd>
                  <dt>Pinned commit</dt>
                  <dd>
                    <code>{openSkill.pinnedCommit.slice(0, 12)}</code>
                  </dd>
                </dl>
                {(openSkill.flags ?? []).length > 0 ? <Notice tone="warn">Flags: {(openSkill.flags ?? []).join(', ')}</Notice> : null}
                {openSkill.status === 'blocked' ? (
                  <ErrorNote error={`Blocked: ${openSkill.blockReason || 'the core did not say why'}. A blocked skill cannot be added.`} />
                ) : have.has(openSkill.name) ? (
                  <p mix={faint}>{pname} already has this skill. Choose it on its Skills page to see who gets it.</p>
                ) : (
                  <div mix={actionsRow}>
                    <Button variant="primary" size="sm" icon={Plus} disabled={act.busy !== '' || pid === ''} data-testid="lib-skill-add" mix={[on('click', () => void add(openSkill))]}>
                      {act.busy === 'add' ? 'Adding...' : `Add to ${pname}`}
                    </Button>
                  </div>
                )}
              </article>
            ) : null}
          </section>
        ) : null}
      </Section>
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
      if (r) say(joinedNote(r.agent.name, project.name, r.granted, r.skipped, serverName, skillLines(r.skills)));
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
  // The current project's skills, to mark the ones a template brings that the project already has.
  const projectSkills = query(handle, {
    load: () => s.client.skillList(s.workspace.projectId!),
    key: () => s.workspace.projectId,
    enabled: () => !!s.workspace.projectId,
    when: (e) => e.type.startsWith('skill.'),
  });
  let dragging: Dragged | undefined;
  let note = '';
  let error: Error | undefined;
  let connecting = false;
  let newTemplate = false;
  // The selected tile of each grid ('' for none); its panel shows below that grid. Clicking the selected tile again closes it.
  let server = '';
  let template = '';
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
    const libraryIds = new Set(libServers.map((x) => x.id));
    const openServer = libServers.find((x) => x.id === server);
    const openTemplate = tpls.find((x) => x.id === template);
    const inProject = new Set((projectSkills.data ?? []).map((k) => k.name));
    const currentName = projects.find((p) => p.id === s.workspace.projectId)?.name;
    return (
      <Page
        title="Library"
        icon={Library}
        wide
        subtitle={servers.data && templates.data ? `${libServers.length} server${libServers.length === 1 ? '' : 's'} · ${tpls.length} template${tpls.length === 1 ? '' : 's'}` : undefined}
      >
        <p mix={introCss}>
          Connect an MCP server or define a team member once, here. Then drag its tile onto a project to add it there, or drag a server onto an agent to let that agent use it.
          Click a tile to see its tools and settings. A project only ever reaches the servers it was given, and an agent only the servers you gave it.
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
                <p mix={emptyLine} data-testid="lib-mcp-empty">
                  <Icon icon={Plug} size={14} />
                  <strong>No servers are connected yet.</strong> Connect a program on this computer or a web address once. Then press Find tools and drag it onto a project.
                </p>
              ) : null}
              {libServers.length > 0 ? (
                <ul mix={tileGrid} aria-label="MCP servers">
                  {libServers.map((x) => (
                    <ServerTile
                      key={x.id}
                      s={x}
                      selected={x.id === server}
                      onSelect={() => set(() => (server = server === x.id ? '' : x.id))}
                      onDragging={onDragging}
                    />
                  ))}
                </ul>
              ) : null}
              {openServer ? <McpPanel key={openServer.id} s={openServer} projects={projects} onDragging={onDragging} say={say} fail={fail} onChanged={reload} /> : null}
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
              {templates.loading && !templates.data ? <Loading /> : null}
              {templates.data && tpls.length === 0 ? (
                <p mix={emptyLine} data-testid="lib-template-empty">
                  <Icon icon={Users} size={14} />
                  <strong>No templates yet.</strong> Make one here, or save an agent as a template on the <a href={routes.team.href()}>Team</a> page.
                </p>
              ) : null}
              {tpls.length > 0 ? (
                <ul mix={tileGrid} aria-label="Agent templates">
                  {tpls.map((x) => (
                    <TemplateTile
                      key={x.id}
                      t={x}
                      libraryIds={libraryIds}
                      selected={x.id === template}
                      onSelect={() => set(() => (template = template === x.id ? '' : x.id))}
                      onDragging={onDragging}
                    />
                  ))}
                </ul>
              ) : null}
              {openTemplate ? (
                <TemplatePanel
                  key={openTemplate.id}
                  t={openTemplate}
                  servers={libServers}
                  projects={projects}
                  adapters={adapterList}
                  inProject={inProject}
                  projectName={currentName}
                  onDragging={onDragging}
                  say={say}
                  onChanged={reload}
                />
              ) : null}
            </Section>

            <SkillsLibrary projects={projects} />

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
                  server = saved.id; // open the new server's panel: Find tools is the next step
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
                  template = saved.id;
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

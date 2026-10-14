// The Library: MCP servers and team members (agent templates) defined once for the whole workspace, and the prebuilt agents.
// Nothing of a single project is shown here (decision P51): a connected server is added to a project, and granted to that
// project's agents, on the project's Integrations page; a team member is made from a template on a project's Team page. Connect
// an MCP server lists the servers built into Drydock first, then a custom server. The skill library has a page of its own
// (SkillLibraryScreen, /library/skills): it fills itself from every enabled registry (decision P48), and adding a skill to a
// project is the person's acceptance.

import { css, on, type Handle } from 'remix/component';
import { BookOpen, Cpu, FolderOpen, GitBranch, Globe, KeyRound, Library, Pencil, Plug, Plus, RefreshCw, Search, Sparkles, Trash2, Unplug, Users } from 'lucide';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import { AgentGallery } from '../team/agent-gallery.tsx';
import type { LibrarySkill, SkillLibraryAddAll } from '../../api/extTypes.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo, Project } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge, faint, kv, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { BrandLogo, brandKey } from '../../ui/brand-logo.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section } from '../../ui/layout.tsx';
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { AddMcpServer } from '../mcp/gallery.tsx';
import { actionsRow, cardHead, confirmCss, serverLogo, ServerMark, subhead, tile, whereCss } from '../mcp/mcp-screen.tsx';
import { OAuthPanel } from '../mcp/oauth-panel.tsx';
import { McpServerForm } from '../mcp/server-form.tsx';
import { inputOf, whereOf } from '../mcp/server-input.ts';
import { addAllText, addedText, libraryBlurb, libraryStateText, listOr, registryStatusText, skillBrand } from '../skills/skills.ts';
import { TemplateForm } from '../team/template-form.tsx';
import { serverBlurb, serverState, templateBlurb, templateState } from './tiles.ts';

type Say = (message: string) => void;
type Fail = (e: Error | undefined) => void;

const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd, maxWidth: '86ch' });
const sectionsCss = css({ display: 'grid', gap: t.s6, minWidth: '0' });
const noteCss = css({ '&:empty': { display: 'none' } });

// The selected entry's panel below its grid.
const panelCard = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
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
    padding: '0 8px',
    borderRadius: t.rFull,
    border: `1px solid ${t.border}`,
    background: t.bgApp,
    fontSize: t.textSm,
  },
});
const skillPanelCss = css({
  display: 'grid',
  gap: t.s3,
  '& h3': { fontFamily: t.mono, fontSize: t.textMd, fontWeight: '600', overflowWrap: 'anywhere' },
  '& p': { margin: '0' },
  '& .reasons': { margin: '0', paddingLeft: '16px', fontSize: t.textSm, color: t.textMuted },
});
const putRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', '& select': { width: 'auto', minWidth: '140px', maxWidth: '100%' } });

function ServerTile(handle: Handle<{ s: McpLibraryServer; selected: boolean; onSelect: () => void }>) {
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
      />
    );
  };
}

function TemplateTile(handle: Handle<{ t: AgentTemplate; libraryIds: ReadonlySet<string>; selected: boolean; onSelect: () => void }>) {
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
      />
    );
  };
}

/** The selected server's full panel: where it runs, its secrets, its sign-in, its tools, and what can be done with it. */
function McpPanel(handle: Handle<{ s: McpLibraryServer; say: Say; fail: Fail; onChanged: () => void }>) {
  const svc = services(handle);
  watch(handle, svc.workspace);
  const act = action(handle);
  let editing = false;
  let confirm = false;
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
    const { s, say, fail, onChanged } = handle.props;
    const ro = svc.workspace.license;
    const n = s.projects.length;
    return (
      <>
        <article mix={[card, panelCard]} data-testid="lib-mcp" data-server={s.id} aria-label={s.display}>
          <header mix={cardHead}>
            <ServerMark s={s} />
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
            {n === 0 ? 'Not in any project yet.' : `In ${n} project${n === 1 ? '' : 's'}.`} Add it to a project, and choose which of its agents may use it, on that project's
            Integrations page.
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
                Disconnect {s.display}? It leaves {n} project(s) and its stored secrets are deleted.
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
function TemplatePanel(handle: Handle<{ t: AgentTemplate; servers: readonly McpLibraryServer[]; adapters: readonly AdapterInfo[]; onChanged: () => void }>) {
  const svc = services(handle);
  const act = action(handle);
  let editing = false;
  let confirm = false;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };

  return () => {
    const { t: tp, servers, adapters, onChanged } = handle.props;
    const nameOf = (id: string) => servers.find((x) => x.id === id)?.display ?? id;
    return (
      <>
        <article mix={[card, panelCard]} data-testid="lib-template" data-template={tp.name} aria-label={tp.name}>
          <header mix={cardHead}>
            <Avatar id={tp.id} name={tp.name} size={30} />
            <h3>{tp.name}</h3>
            {tp.role ? <span mix={muted}>{tp.role}</span> : null}
            <code mix={faint}>{tp.adapter}</code>
            {tp.model ? <Badge>model {tp.model}</Badge> : null}
            {tp.effort ? <Badge>effort {tp.effort}</Badge> : null}
          </header>
          <ErrorNote error={act.error} what="Not changed" />
          {tp.purpose ? <p>{tp.purpose.length > 200 ? `${tp.purpose.slice(0, 200)}...` : tp.purpose}</p> : <p mix={muted}>No purpose written.</p>}
          {tp.mcpServers.length > 0 ? (
            <ul mix={chips} aria-label="MCP servers it gets">
              {tp.mcpServers.map((id) => (
                <li key={id}>
                  <Icon icon={Plug} size={12} />
                  {nameOf(id)}
                </li>
              ))}
            </ul>
          ) : null}
          {tp.skills.length > 0 ? (
            <ul mix={chips} aria-label="Skills it brings">
              {tp.skills.map((k) => (
                <li key={k.name} title={`${k.registry}${k.ref ? ` at ${k.ref.slice(0, 12)}` : ''}`}>
                  <Icon icon={Cpu} size={12} />
                  {k.name}
                </li>
              ))}
            </ul>
          ) : null}
          <p mix={faint} data-testid="lib-template-where">
            A project gets a team member from this template on its <a href={routes.team.href()}>Team</a> page.
          </p>
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

export function LibraryScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const adapters = query(handle, { load: () => s.client.adapterList() });
  const servers = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });
  const templates = query(handle, { load: () => s.client.agentTemplateList(), when: (e) => e.type.startsWith('agenttemplate.') });
  const prebuilt = query(handle, { load: () => s.client.agentCatalogList() });
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
  const reload = () => {
    servers.reload();
    templates.reload();
  };

  return () => {
    const libServers = servers.data ?? [];
    const tpls = templates.data ?? [];
    const adapterList = adapters.data ?? [];
    const libraryIds = new Set(libServers.map((x) => x.id));
    const openServer = libServers.find((x) => x.id === server);
    const openTemplate = tpls.find((x) => x.id === template);
    return (
      <Page
        title="Library"
        icon={Library}
        wide
        subtitle={servers.data && templates.data ? `${libServers.length} server${libServers.length === 1 ? '' : 's'} · ${tpls.length} template${tpls.length === 1 ? '' : 's'}` : undefined}
      >
        <div mix={sectionsCss}>
          <p mix={introCss}>
            Connect an MCP server or define a team member once, here, for the whole workspace. Click a tile to see its tools and settings. A project gets a server on its own
            Integrations page, where you also choose which of its agents may use it, and a team member from a template on its Team page: a project only ever reaches the servers it
            was given, and an agent only the servers you gave it. Skills have a page of their own: <a href={routes.librarySkills.href()}>Skills</a>.
          </p>
          <div role="status" aria-live="polite" data-testid="library-note" mix={noteCss}>
            {note ? <Notice tone="ok">{note}</Notice> : null}
          </div>
          <ErrorNote error={error ?? servers.error ?? templates.error} what="Library" />
          <Section
            title="MCP servers"
            description="Connected once for the whole workspace: one built into Drydock, or your own. Secrets stay in the Windows credential store."
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
                <strong>No servers are connected yet.</strong> Connect one built into Drydock, or a program on this computer or a web address of your own. Then press Find tools.
              </p>
            ) : null}
            {libServers.length > 0 ? (
              <ul mix={tileGrid} aria-label="MCP servers">
                {libServers.map((x) => (
                  <ServerTile key={x.id} s={x} selected={x.id === server} onSelect={() => set(() => (server = server === x.id ? '' : x.id))} />
                ))}
              </ul>
            ) : null}
            {openServer ? <McpPanel key={openServer.id} s={openServer} say={say} fail={fail} onChanged={reload} /> : null}
          </Section>

          <Section
            title="Agent templates"
            description="A team member defined once. Applying it on a project's Team page makes an ordinary agent in that project."
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
                <strong>No templates yet.</strong> Make one here, or save an agent as a template on a project's <a href={routes.team.href()}>Team</a> page.
              </p>
            ) : null}
            {tpls.length > 0 ? (
              <ul mix={tileGrid} aria-label="Agent templates">
                {tpls.map((x) => (
                  <TemplateTile key={x.id} t={x} libraryIds={libraryIds} selected={x.id === template} onSelect={() => set(() => (template = template === x.id ? '' : x.id))} />
                ))}
              </ul>
            ) : null}
            {openTemplate ? <TemplatePanel key={openTemplate.id} t={openTemplate} servers={libServers} adapters={adapterList} onChanged={reload} /> : null}
          </Section>

          <Section
            title="Prebuilt agents"
            description={
              <>
                Built into Drydock, with their skills and connectors pinned. Add them to a project on its <a href={routes.team.href()}>Team</a> page or when you create a project.
              </>
            }
            testId="library-prebuilt"
          >
            <ErrorNote error={prebuilt.error} what="Could not load the prebuilt agents" />
            {prebuilt.loading && !prebuilt.data ? <Loading /> : null}
            <AgentGallery entries={prebuilt.data ?? []} label="Prebuilt agents" />
          </Section>
        </div>

        <Dialog open={connecting} title="Connect an MCP server" wide onClose={() => set(() => (connecting = false))}>
          {connecting ? (
            <AddMcpServer
              existingIds={libServers.map((x) => x.id)}
              onChanged={reload}
              onSaved={(saved) => {
                set(() => {
                  connecting = false;
                  server = saved.id; // open the new server's panel: Find tools is the next step
                  note = `${saved.display} is connected. Press Find tools, then add it to a project on that project's Integrations page.`;
                });
                reload();
              }}
              onImported={(n) => {
                set(() => {
                  connecting = false;
                  note = `${n} server${n === 1 ? '' : 's'} imported.`;
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

/** The workspace's skill library on a page of its own (/library/skills): every registry's skills, added to any project from here. */
export function SkillLibraryScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  return () => {
    const projects = s.workspace.projectList;
    return (
      <Page title="Skills" icon={Cpu} wide testId="skill-library">
        <p mix={introCss}>
          Instructions agents read, from the skill registries you enabled. Pick a registry, then add a skill to a project. A project's own skills (who gets each, usage, the
          ones found in its repository) are on its Skills page.
        </p>
        {s.workspace.projects.data && projects.length === 0 ? (
          <Notice tone="info" testId="skill-library-no-project">
            Skills are added to a project. <a href={routes.projectNew.href()}>Create a project</a> first; you can browse the registries meanwhile.
          </Notice>
        ) : null}
        <SkillsLibrary projects={projects} />
      </Page>
    );
  };
}

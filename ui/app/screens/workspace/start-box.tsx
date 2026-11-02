// The start screen's box (decision P142): one description, a row of chips under it (Where, Who, model · effort, permissions),
// Start (Enter), the progress in the box and one line of error under it; then the "Or:" row (New folder, Clone from GitHub,
// Open a folder..., Just chat). What Enter does is start.ts; this file only holds the choices and shows them. A project without
// work yet is Home's New project (P143); the box has no way to the old wizard any more.

import { css, navigate, on, ref, type Handle, type MixInput, type RemixNode } from 'remix/component';
import { ArrowDownToLine, Check, ChevronDown, CornerDownLeft, FolderOpen, FolderPlus, MessagesSquare, Plus, Users } from 'lucide';
import type { RepoFolder } from '../../api/folderClient.ts';
import type { AgentDraft } from '../../api/libraryClient.ts';
import type { GitHubRepo } from '../../api/types.ts';
import { projectWorkHref } from '../../lib/project-work.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { hueFor } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { Popover } from '../../ui/popover.tsx';
import { Segmented } from '../../ui/segmented.tsx';
import { t } from '../../ui/tokens.ts';
import { FolderField } from '../project/folder-field.tsx';
import { RepoPicker } from '../project/repo-picker.tsx';
import { cloneTarget, folderProblem, currentCheck } from '../project/wizard.ts';
import { DescribeAgent } from '../team/new-agent.tsx';
import { effortList, modelList, PERMISSION_SEGMENTS } from '../team/agent-edit.ts';
import {
  browserStore,
  builderAdapter,
  crewFor,
  crewKinds,
  crewLabel,
  defaultTuning,
  newFolderPath,
  permissionLabel,
  planStart,
  recall,
  remember,
  runJustChat,
  runStart,
  StartError,
  tuningApplies,
  tuningLabel,
  whereKey,
  whereLabel,
  type Crew,
  type CrewKind,
  type DraftedMember,
  type Tuning,
  type Where,
} from './start.ts';

const boxCss = css({
  display: 'grid',
  gap: t.s3,
  padding: `${t.s4} ${t.s4} ${t.s3}`,
  border: `1px solid ${t.borderStrong}`,
  borderRadius: t.rXl,
  background: t.bgSurface,
  boxShadow: `0 18px 50px -30px ${t.scrim}`,
  '&:focus-within': { borderColor: t.textFaint },
  '& textarea': {
    width: '100%',
    minHeight: '76px',
    maxHeight: '320px',
    resize: 'vertical',
    padding: '0',
    border: '0',
    background: 'transparent',
    fontSize: t.textLg,
    lineHeight: '1.5',
    boxShadow: 'none',
  },
  '& textarea:focus-visible': { boxShadow: 'none', outline: 'none' },
});
const barCss = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '6px' });
const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  maxWidth: '210px',
  height: '28px',
  minHeight: '0',
  padding: '0 8px 0 10px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  background: t.bgRaised,
  color: t.text,
  fontSize: t.textSm,
  '& .n': { fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .d': { color: t.textMuted, fontWeight: '400' },
  '& svg': { flex: 'none', color: t.textMuted },
  '&:hover:not(:disabled)': { background: t.bgHover },
  '&[aria-expanded="true"]': { borderColor: t.accent },
});
const endCss = css({ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: t.s2, '& small': { color: t.textFaint, fontSize: t.textXs, whiteSpace: 'nowrap' } });
const progressCss = css({ color: t.textMuted, fontSize: t.textSm });
const errorLine = css({ margin: '0', color: t.danger, fontSize: t.textSm });
const orCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: '6px',
  color: t.textFaint,
  fontSize: t.textSm,
  '& button, & a': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    height: '28px',
    minHeight: '0',
    padding: '0 10px',
    border: `1px solid ${t.border}`,
    borderRadius: t.rFull,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    textDecoration: 'none',
  },
  '& button:hover, & a:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
});
const menuCss = css({
  display: 'grid',
  gap: '2px',
  '& h3': { margin: `${t.s1} 0 2px`, padding: '0 8px', color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' },
  '& hr': { width: '100%', margin: `${t.s1} 0`, border: '0', borderTop: `1px solid ${t.border}` },
  '& button.item, & a.item': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    width: '100%',
    minHeight: '32px',
    padding: '4px 8px',
    border: '0',
    borderRadius: t.rMd,
    background: 'transparent',
    color: t.text,
    fontSize: t.textMd,
    textAlign: 'left',
    textDecoration: 'none',
  },
  '& .item:hover': { background: t.bgHover, textDecoration: 'none' },
  '& .item[aria-pressed="true"]': { background: t.bgActive },
  '& .item .grow': { flex: '1', minWidth: '0', display: 'grid', gap: '1px' },
  '& .item .sub': { color: t.textMuted, fontSize: t.textXs },
  '& .item .path': { color: t.textFaint, fontFamily: t.mono, fontSize: t.textXs, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', maxWidth: '160px' },
  '& .dot': { width: '8px', height: '8px', borderRadius: '2px', flex: 'none' },
  '& input.path-input': { width: '100%', fontFamily: t.mono, fontSize: t.textSm },
  '& .row': { display: 'grid', gap: '4px', padding: '4px 8px' },
});
const dialogBody = css({ display: 'grid', gap: t.s4, minWidth: '0', '& .acts': { display: 'flex', justifyContent: 'flex-end', gap: t.s2 } });

const noTuning: Tuning = { model: '', effort: '', permission: 'acceptEdits' };

/** A chip: an icon, a name and a quiet detail, opening its menu. */
function chip(mix: MixInput<HTMLElement>, expanded: boolean, testId: string, label: string, icon: IconNode | undefined, name: string, detail?: string, disabled?: boolean): RemixNode {
  return (
    <button type="button" mix={[chipCss, mix]} data-testid={testId} aria-haspopup="dialog" aria-expanded={expanded ? 'true' : 'false'} aria-label={`${label}: ${name}${detail ? ` ${detail}` : ''}`} disabled={disabled}>
      {icon ? <Icon icon={icon} size={13} /> : null}
      <span class="n">
        {name}
        {detail ? <span class="d"> · {detail}</span> : null}
      </span>
      <Icon icon={ChevronDown} size={12} />
    </button>
  );
}

export function StartBox(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const store = browserStore();
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href).catch(() => {}));

  let text = '';
  let where: Where = { kind: 'new', path: '' };
  /** The new folder as the person typed it; undefined: it follows the description. */
  let newPath: string | undefined;
  let crew: Crew = { kind: 'solo', templates: [], drafted: [] };
  let tuning: Tuning | undefined; // undefined until chosen or remembered: the builder's defaults then
  let menu: '' | 'where' | 'who' | 'model' | 'perm' = '';
  let dialog: '' | 'folder' | 'clone' | 'describe' | 'mine' = '';
  let progress = '';
  let error = '';
  // the open-a-folder and clone dialogs' own state
  let folder = { path: '', check: undefined as RepoFolder | undefined, init: false };
  let clone = { repo: undefined as GitHubRepo | undefined, cloneDir: '' };
  let picked: string[] = [];
  let box: HTMLTextAreaElement | undefined;

  const catalog = query(handle, { load: () => s.client.agentCatalogList() });
  const adapters = query(handle, { load: () => s.client.adapterList(), when: (e) => e.type.startsWith('connection.') || e.type.startsWith('adapter.') });
  const baseDir = query(handle, { load: () => s.client.githubCloneDir(true).catch(() => '') });
  const templates = query(handle, { enabled: () => dialog === 'mine' || crew.kind === 'mine', load: () => s.client.agentTemplateList(), when: (e) => e.type.startsWith('agenttemplate.') });
  const projectId = () => (where.kind === 'project' ? where.projectId : '');
  const crewOf = query(handle, {
    enabled: () => !!projectId(),
    key: () => projectId(),
    load: () => s.client.agentList(projectId()),
    when: (e) => e.type.startsWith('agent.'),
  });

  const update = () => void handle.update();
  const set = (f: () => void) => (f(), update());

  /** The choices remembered for a place (or nothing), applied to the chips. */
  const applyRemembered = (w: Where) => {
    const r = recall(store, whereKey(w)) ?? (w.kind === 'project' ? undefined : recall(store, 'new'));
    crew = { kind: crewFor(w, r?.crew), templates: r?.templates ?? [], drafted: [] };
    tuning = r ? { model: r.model, effort: r.effort, permission: r.permission } : tuning;
  };
  applyRemembered(where);

  const choose = (w: Where) =>
    set(() => {
      where = w;
      menu = '';
      error = '';
      applyRemembered(w);
      handle.queueTask(() => box?.focus());
    });

  const effectiveWhere = (): Where => (where.kind === 'new' ? { kind: 'new', path: newPath ?? (baseDir.data ? newFolderPath(baseDir.data, text) : '') } : where);
  const tuned = (): Tuning => tuning ?? (catalog.data ? defaultTuning(catalog.data) : noTuning);
  const adapterId = () => builderAdapter(catalog.data ?? [], adapters.data?.find((a) => a.detection.installed)?.id ?? 'claude');

  const start = async () => {
    if (act.busy) return;
    error = '';
    const plan = planStart({
      where: effectiveWhere(),
      text,
      crew,
      tuning: tuned(),
      catalog: catalog.data ?? [],
      templates: templates.data ?? [],
      projects: s.workspace.projectList,
      fallbackAdapter: adapterId(),
    });
    if ('problem' in plan) return set(() => (error = plan.problem));
    const w = effectiveWhere();
    // a first run shows this box in place of the project's pages: the new project must not replace it before the work is made
    if (plan.setup) s.workspace.setSetupOpen(true);
    const out = await act.run('start', async () => {
      try {
        return await runStart(s.client, plan, (step) => set(() => (progress = step)), () => s.workspace.projects.reload());
      } catch (e) {
        // the project exists: another try works there instead of making a second one
        if (e instanceof StartError && e.projectId && where.kind !== 'project') where = { kind: 'project', projectId: e.projectId };
        throw e;
      }
    });
    progress = '';
    s.workspace.setSetupOpen(false);
    if (!out) {
      error = act.error?.message ?? 'Could not start.';
      return update();
    }
    const tu = tuned();
    remember(store, [whereKey(w), `project:${out.project.id}`], { crew: crew.kind === 'project' ? 'project' : crew.kind, templates: crew.templates, model: tu.model, effort: tu.effort, permission: tu.permission });
    if (out.warnings.length) s.ui.flash(`${out.project.name}: ${out.warnings.join(' ')}`, 'warn');
    text = '';
    newPath = undefined;
    s.workspace.selectProject(out.project.id);
    s.workspace.projects.reload();
    go(projectWorkHref(out.item));
  };

  const justChat = async () => {
    if (act.busy) return;
    error = '';
    const settings = tuningApplies(crew) ? { adapter: adapterId(), model: tuned().model, effort: tuned().effort, permissionMode: tuned().permission } : {};
    for (const k of Object.keys(settings) as (keyof typeof settings)[]) if (!settings[k]) delete settings[k];
    progress = 'Starting chat…';
    const id = await act.run('chat', () => runJustChat(s.client, text, settings));
    progress = '';
    if (!id) return set(() => (error = act.error?.message ?? 'Could not start the chat.'));
    text = '';
    go(routes.chat.href({ id }));
  };

  const addDraft = (d: AgentDraft, confirmed: string) =>
    set(() => {
      const m: DraftedMember = { name: d.name, role: d.role, adapter: d.adapter, model: d.model, effort: d.effort, permissionMode: d.permissionMode, purpose: d.purpose, tools: [...d.tools], confirmed };
      crew = { ...crew, drafted: [...crew.drafted, m] };
      dialog = '';
    });

  // ---- the menus ----

  const whereMenu = () => {
    const recent = [...s.workspace.projectList].sort((a, b) => b.created - a.created).slice(0, 6);
    const w = effectiveWhere();
    return (
      <div mix={menuCss}>
        <h3>Where</h3>
        <button type="button" class="item" aria-pressed={where.kind === 'new' ? 'true' : 'false'} data-testid="where-new" mix={on('click', () => choose({ kind: 'new', path: '' }))}>
          <Icon icon={Plus} size={14} />
          <span class="grow">New folder</span>
          {w.kind === 'new' && w.path ? <span class="path">{w.path}</span> : null}
        </button>
        {where.kind === 'new' ? (
          <div class="row">
            <input
              class="path-input"
              aria-label="New folder"
              data-testid="where-new-path"
              spellcheck={false}
              autocomplete="off"
              value={w.kind === 'new' ? w.path : ''}
              placeholder="C:\Users\you\source\repos\my-app"
              mix={on('input', (e) => set(() => (newPath = e.currentTarget.value)))}
            />
          </div>
        ) : null}
        <button type="button" class="item" data-testid="where-folder" mix={on('click', () => set(() => ((menu = ''), (dialog = 'folder'))))}>
          <Icon icon={FolderOpen} size={14} />
          <span class="grow">Open a folder…</span>
        </button>
        <button type="button" class="item" data-testid="where-clone" mix={on('click', () => set(() => ((menu = ''), (dialog = 'clone'))))}>
          <Icon icon={ArrowDownToLine} size={14} />
          <span class="grow">Clone from GitHub…</span>
        </button>
        {recent.length ? (
          <>
            <hr />
            <h3>Recent</h3>
            {recent.map((p) => (
              <button
                key={p.id}
                type="button"
                class="item"
                data-testid="where-recent"
                data-project={p.id}
                aria-pressed={where.kind === 'project' && where.projectId === p.id ? 'true' : 'false'}
                mix={on('click', () => choose({ kind: 'project', projectId: p.id }))}
              >
                <span class="dot" style={{ background: `hsl(${hueFor(p.id)} 62% 62%)` }} aria-hidden="true" />
                <span class="grow">{p.name}</span>
                {p.repoPath ? <span class="path">{p.repoPath}</span> : null}
              </button>
            ))}
          </>
        ) : null}
      </div>
    );
  };

  const WHO: Record<CrewKind, [string, string]> = {
    solo: ['Solo', 'One agent, in a chat. Fastest.'],
    team: ['Small team', 'Builder + reviewer + lead. Checked work.'],
    mine: ['My agents…', 'Pick from your saved agents.'],
    project: ['Project crew', "The project's own agents, on a task."],
  };
  const whoMenu = () => (
    <div mix={menuCss}>
      <h3>Who works on it</h3>
      {crewKinds(where).map((k) => (
        <button
          key={k}
          type="button"
          class="item"
          data-testid={`who-${k}`}
          aria-pressed={crew.kind === k ? 'true' : 'false'}
          mix={on('click', () =>
            set(() => {
              menu = '';
              if (k === 'mine') {
                picked = [...crew.templates];
                dialog = 'mine';
              } else crew = { ...crew, kind: k };
            }),
          )}
        >
          <span class="grow">
            <span>{WHO[k][0]}</span>
            <span class="sub">{k === 'solo' ? `One ${crewLabel({ kind: 'solo', templates: [], drafted: [] }, { adapter: adapterId(), catalog: [] }).detail} agent, in a chat.` : WHO[k][1]}</span>
          </span>
          {crew.kind === k ? <Icon icon={Check} size={14} /> : null}
        </button>
      ))}
      <hr />
      <button type="button" class="item" data-testid="who-describe" mix={on('click', () => set(() => ((menu = ''), (dialog = 'describe'))))}>
        <Icon icon={Plus} size={14} />
        <span class="grow">Describe a new agent…</span>
      </button>
    </div>
  );

  const modelMenu = () => {
    const a = adapterId();
    const caps = adapters.data?.find((x) => x.id === a)?.capabilities;
    const tu = tuned();
    const models = modelList(a, caps);
    const efforts = effortList(a, caps);
    const tune = (p: Partial<Tuning>) => set(() => (tuning = { ...tu, ...p }));
    return (
      <div mix={menuCss}>
        <h3>Model</h3>
        {['', ...models, ...(tu.model && !models.includes(tu.model) ? [tu.model] : [])].map((m) => (
          <button key={m || 'default'} type="button" class="item" data-testid="model-option" data-value={m} aria-pressed={tu.model === m ? 'true' : 'false'} mix={on('click', () => tune({ model: m }))}>
            <span class="grow">{m || 'Default'}</span>
            {tu.model === m ? <Icon icon={Check} size={14} /> : null}
          </button>
        ))}
        <h3>Effort</h3>
        <div class="row">
          <Segmented label="Effort" value={tu.effort} options={[{ value: '', label: 'default' }, ...efforts.map((e) => ({ value: e, label: e }))]} onChange={(v) => tune({ effort: v })} testId="effort-options" />
        </div>
      </div>
    );
  };

  const permMenu = () => {
    const tu = tuned();
    return (
      <div mix={menuCss}>
        <h3>Permissions</h3>
        {PERMISSION_SEGMENTS.map((p) => (
          <button
            key={p.value}
            type="button"
            class="item"
            data-testid="perm-option"
            data-value={p.value}
            aria-pressed={tu.permission === p.value ? 'true' : 'false'}
            mix={on('click', () => set(() => ((tuning = { ...tu, permission: p.value }), (menu = ''))))}
          >
            <span class="grow">
              <span>{p.label}</span>
              <span class="sub">{p.hint}</span>
            </span>
            {tu.permission === p.value ? <Icon icon={Check} size={14} /> : null}
          </button>
        ))}
      </div>
    );
  };

  const popover = (name: 'where' | 'who' | 'model' | 'perm', label: string, trigger: (mix: MixInput<HTMLElement>) => RemixNode, body: () => RemixNode) => (
    <Popover
      open={menu === name}
      onOpen={() => set(() => (menu = name))}
      onClose={() => set(() => (menu = menu === name ? '' : menu))}
      label={label}
      testId={`menu-${name}`}
      stopOutsideClick={false}
      trigger={trigger}
    >
      {menu === name ? body() : null}
    </Popover>
  );

  return () => {
    const busy = !!act.busy;
    const w = effectiveWhere();
    const tu = tuned();
    const projects = s.workspace.projectList;
    const cl = crewLabel(crew, { adapter: adapterId(), catalog: catalog.data ?? [], projectAgents: crewOf.data?.length });
    const fp = folderProblem(currentCheck({ localPath: folder.path, localCheck: folder.check }), folder.init);
    return (
      <div style={{ display: 'grid', gap: 'var(--space-3)' }} data-testid="start">
        <form
          mix={[
            boxCss,
            on('submit', (e) => {
              e.preventDefault();
              void start();
            }),
          ]}
          aria-label="What do you want to build?"
          data-testid="start-box"
          aria-busy={busy ? 'true' : undefined}
        >
          <textarea
            aria-label="Describe what you want to build"
            placeholder="Describe it. Enter to start, Shift+Enter for a new line."
            data-testid="start-text"
            data-autofocus
            autofocus
            readOnly={busy}
            value={text}
            mix={[
              ref((n) => {
                box = n as HTMLTextAreaElement;
                requestAnimationFrame(() => box?.focus());
              }),
              on('input', (e) => set(() => ((text = e.currentTarget.value), (error = '')))),
              on('keydown', (e) => {
                const k = e as KeyboardEvent;
                if (k.key === 'Enter' && !k.shiftKey && !k.isComposing && !k.ctrlKey && !k.altKey && !k.metaKey) {
                  k.preventDefault();
                  void start();
                }
              }),
            ]}
          />
          <div mix={barCss}>
            {popover('where', 'Where', (mix) => chip(mix, menu === 'where', 'start-where', 'Where', where.kind === 'new' ? FolderPlus : FolderOpen, whereLabel(w, projects), undefined, busy), whereMenu)}
            {popover('who', 'Who works on it', (mix) => chip(mix, menu === 'who', 'start-who', 'Who', Users, cl.name, cl.detail, busy), whoMenu)}
            {tuningApplies(crew) ? (
              <>
                {popover('model', 'Model and effort', (mix) => chip(mix, menu === 'model', 'start-model', 'Model and effort', undefined, tuningLabel(tu), undefined, busy), modelMenu)}
                {popover('perm', 'Permissions', (mix) => chip(mix, menu === 'perm', 'start-permission', 'Permissions', undefined, permissionLabel(tu.permission), undefined, busy), permMenu)}
              </>
            ) : null}
            <span mix={endCss}>
              {progress ? (
                <span role="status" mix={progressCss} data-testid="start-progress">
                  {progress}
                </span>
              ) : (
                <small aria-hidden="true">Enter to start</small>
              )}
              <Button type="submit" variant="primary" size="sm" disabled={busy || s.workspace.license.readOnly} title={s.workspace.license.readOnly ? s.workspace.license.reason : undefined} data-testid="start-button" aria-keyshortcuts="Enter">
                Start <Icon icon={CornerDownLeft} size={13} />
              </Button>
            </span>
          </div>
        </form>
        {error ? (
          <p role="alert" mix={errorLine} data-testid="start-error">
            {error}
          </p>
        ) : null}
        <div mix={orCss} data-testid="start-or">
          <span>Or:</span>
          <button type="button" data-testid="or-new-folder" disabled={busy} mix={on('click', () => choose({ kind: 'new', path: '' }))}>
            <Icon icon={Plus} size={13} /> New folder
          </button>
          <button type="button" data-testid="or-clone" disabled={busy} mix={on('click', () => set(() => (dialog = 'clone')))}>
            <Icon icon={ArrowDownToLine} size={13} /> Clone from GitHub
          </button>
          <button type="button" data-testid="or-folder" disabled={busy} mix={on('click', () => set(() => (dialog = 'folder')))}>
            <Icon icon={FolderOpen} size={13} /> Open a folder…
          </button>
          <button type="button" data-testid="or-chat" disabled={busy} mix={on('click', () => void justChat())}>
            <Icon icon={MessagesSquare} size={13} /> Just chat
          </button>
        </div>

        <Dialog open={dialog === 'folder'} title="Open a folder" onClose={() => set(() => (dialog = ''))} testId="start-folder-dialog">
          {dialog === 'folder' ? (
            <div mix={dialogBody}>
              <FolderField
                label="Folder"
                value={folder.path}
                onChange={(v) => set(() => (folder = { ...folder, path: v }))}
                check={folder.check}
                onCheck={(c) => set(() => (folder = { ...folder, check: c }))}
                initRepo={folder.init}
                onInitRepo={(v) => set(() => (folder = { ...folder, init: v }))}
              />
              <div class="acts">
                <Button variant="secondary" mix={on('click', () => set(() => (dialog = '')))}>
                  Cancel
                </Button>
                <Button
                  variant="primary"
                  data-testid="folder-use"
                  disabled={!folder.path.trim() || !!fp}
                  title={fp}
                  mix={on('click', () => {
                    dialog = '';
                    choose({ kind: 'folder', path: folder.path.trim().replace(/^"+|"+$/g, ''), init: folder.init });
                  })}
                >
                  Use this folder
                </Button>
              </div>
            </div>
          ) : null}
        </Dialog>

        <Dialog open={dialog === 'clone'} title="Clone from GitHub" wide onClose={() => set(() => (dialog = ''))} testId="start-clone-dialog">
          {dialog === 'clone' ? (
            <div mix={dialogBody}>
              <RepoPicker selected={clone.repo} cloneDir={clone.cloneDir} onSelect={(r) => set(() => (clone = { ...clone, repo: r }))} onCloneDir={(d) => set(() => (clone = { ...clone, cloneDir: d }))} unavailableHint="Choose New folder or Open a folder instead." />
              <div class="acts">
                <Button variant="secondary" mix={on('click', () => set(() => (dialog = '')))}>
                  Cancel
                </Button>
                <Button
                  variant="primary"
                  data-testid="clone-use"
                  disabled={!clone.repo || !clone.cloneDir.trim()}
                  title={clone.repo ? cloneTarget(clone.cloneDir, clone.repo) : undefined}
                  mix={on('click', () => {
                    if (!clone.repo) return;
                    dialog = '';
                    choose({ kind: 'clone', repo: clone.repo.fullName, cloneDir: clone.cloneDir.trim() });
                  })}
                >
                  Use this repository
                </Button>
              </div>
            </div>
          ) : null}
        </Dialog>

        <Dialog open={dialog === 'mine'} title="My agents" onClose={() => set(() => (dialog = ''))} testId="start-mine-dialog">
          {dialog === 'mine' ? (
            <div mix={dialogBody}>
              <ErrorNote error={templates.error} what="Your agents" />
              {templates.data && templates.data.length === 0 ? (
                <p style={{ margin: '0', color: 'var(--text-muted)' }}>
                  No saved agents yet. <a href={routes.library.href()}>Agents</a>
                </p>
              ) : null}
              <div style={{ display: 'grid', gap: 'var(--space-1)' }}>
                {(templates.data ?? []).map((tp) => (
                  <Checkbox
                    key={tp.id}
                    label={tp.name}
                    description={[tp.role, tp.adapter, tp.model, tp.effort].filter(Boolean).join(' · ')}
                    checked={picked.includes(tp.id)}
                    onChange={(v) => set(() => (picked = v ? [...picked, tp.id] : picked.filter((x) => x !== tp.id)))}
                  />
                ))}
              </div>
              <div class="acts">
                <Button variant="secondary" mix={on('click', () => set(() => (dialog = '')))}>
                  Cancel
                </Button>
                <Button variant="primary" data-testid="mine-use" disabled={picked.length === 0} mix={on('click', () => set(() => ((crew = { ...crew, kind: 'mine', templates: [...picked] }), (dialog = ''))))}>
                  Use {picked.length || ''} {picked.length === 1 ? 'agent' : 'agents'}
                </Button>
              </div>
            </div>
          ) : null}
        </Dialog>

        <Dialog open={dialog === 'describe'} title="Describe a new agent" wide onClose={() => set(() => (dialog = ''))} testId="start-describe-dialog">
          {dialog === 'describe' ? (
            <DescribeAgent
              projectId={projectId()}
              adapters={adapters.data ?? []}
              onCreated={() => set(() => ((dialog = ''), (crew = { ...crew, kind: 'project' })))}
              onDraft={where.kind === 'project' ? undefined : addDraft}
            />
          ) : null}
        </Dialog>
      </div>
    );
  };
}

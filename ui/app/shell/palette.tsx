// The command menu (docs/ui.md 6.3; the graphite mockup "Command menu", decision P97), Ctrl+K or the sidebar's search: a
// modal <dialog> over the dimmed page. A chip names the task the menu acts on (the task of the page: its own page, its
// workflow, its room), the input drives a list of grouped options (aria-activedescendant) ranked by palette-items.ts, and
// the "What happens" panel beside it says what the highlighted option does: for an assignment, the core's own preview
// (task.assignPreview: the branch, the spec gate, the checks, what it asks before, the agent's queue).

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { ArrowRight, Bot, ClipboardList, Inbox, MessageCircle, MessagesSquare, Search, Shield, User, Users, Zap, type IconNode } from 'lucide';
import type { Agent, Connection, Task } from '../api/types.ts';
import { projectHasRepo } from '../api/types.ts';
import type { AssignPreview, PreviewAgent } from '../api/controlClient.ts';
import type { ProjectRunMode } from '../api/wslClient.ts';
import { ownsKeys, shortcutOf } from '../lib/keys.ts';
import { taskSearchNote } from '../lib/task-total.ts';
import type { ThemeChoice } from '../lib/theme.ts';
import { routes } from '../routes.ts';
import { chatChoices, pickChatChoice, type ChatChoice } from '../screens/chats/chats.ts';
import { cliName } from '../screens/shared/cli-name.ts';
import { action, query as liveQuery, services, watch } from '../state/services.tsx';
import type { DockTab } from '../state/ui.ts';
import { Avatar } from '../ui/data.tsx';
import { ErrorNote } from '../ui/feedback.tsx';
import { Icon } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';
import {
  GO_CHORDS,
  PALETTE_CHATS,
  arrange,
  buildItems,
  filterItems,
  isChatsPath,
  previewBlock,
  previewSteps,
  previewWait,
  taskKey,
  type PaletteItem,
  type TaskContext,
} from './palette-items.ts';

const dialogCss = css({
  width: 'min(880px, calc(100vw - 32px))',
  maxWidth: '880px',
  maxHeight: 'calc(100vh - 32px)',
  marginTop: 'min(110px, 10vh)',
  padding: '0',
  border: `1px solid ${t.borderStrong}`,
  borderRadius: t.rXl,
  background: t.bgSurface,
  color: t.text,
  boxShadow: t.shadowLg,
  overflow: 'hidden',
  '&[open]': { display: 'flex', flexDirection: 'column', animation: `dd-pop ${t.med} ${t.ease}` },
  '&::backdrop': { background: t.scrim },
  '@keyframes dd-pop': { from: { opacity: '0', transform: 'translateY(-6px) scale(0.98)' }, to: { opacity: '1', transform: 'none' } },
});
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  padding: `${t.s3} ${t.s4}`,
  borderBottom: `1px solid ${t.border}`,
  color: t.textMuted,
  '& input': {
    flex: '1',
    minWidth: '0',
    height: '32px',
    padding: '0',
    border: '0',
    background: 'transparent',
    color: t.text,
    fontSize: t.textXl,
    boxShadow: 'none',
  },
  '& input:focus-visible': { boxShadow: 'none', border: '0', outline: 'none' },
});
const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  flex: 'none',
  maxWidth: '40%',
  height: '24px',
  padding: '0 8px',
  borderRadius: t.rSm,
  background: t.bgActive,
  color: t.textMuted,
  fontSize: t.textXs,
  whiteSpace: 'nowrap',
  '& .key': { flex: 'none', color: t.textFaint, fontFamily: t.mono, fontWeight: '500' },
  '& .title': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis' },
});
const bodyCss = css({ display: 'flex', flexWrap: 'wrap', minHeight: 'min(420px, 50vh)', flex: '1', overflowY: 'auto' });
const listCss = css({
  flex: '3 1 420px',
  minWidth: '0',
  maxHeight: 'min(460px, 58vh)',
  overflowY: 'auto',
  padding: '6px 8px 10px',
  borderRight: `1px solid ${t.border}`,
});
const groupCss = css({ padding: '10px 12px 4px', color: t.textFaint, fontSize: t.textXs, fontWeight: '500', letterSpacing: '0.04em', textTransform: 'uppercase' });
const optCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  minHeight: '38px',
  padding: '0 12px',
  borderRadius: t.rMd,
  color: t.text,
  fontSize: t.textMd,
  cursor: 'pointer',
  '& .lbl': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .hint': { flex: 'none', color: t.textFaint, fontSize: t.textXs, whiteSpace: 'nowrap' },
  '& .ico': { display: 'inline-flex', color: t.textMuted },
  '& .enter': { display: 'none' },
  '&:hover': { background: t.bgHover },
  '&[aria-selected="true"]': { background: t.bgActive, boxShadow: `inset 2px 0 0 ${t.accent}` },
  '&[aria-selected="true"] .enter': { display: 'inline-flex' },
});
const emptyCss = css({ padding: t.s6, textAlign: 'center', color: t.textMuted });
const asideCss = css({
  flex: '2 1 280px',
  minWidth: '0',
  padding: '18px 20px',
  display: 'flex',
  flexDirection: 'column',
  gap: '14px',
  background: t.bgSidebar,
  fontSize: t.textMd,
  '& p': { margin: '0', color: t.textMuted, lineHeight: '1.5' },
});
const whoCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  '& .who': { display: 'flex', flexDirection: 'column', gap: '2px', minWidth: '0' },
  '& .name': { fontWeight: '600' },
  '& .sub': { color: t.textFaint, fontSize: t.textXs },
});
const stepsCss = css({
  margin: '0',
  padding: '0 0 0 18px',
  display: 'flex',
  flexDirection: 'column',
  gap: t.s2,
  color: t.textMuted,
  lineHeight: '1.5',
  '& code': { padding: '0', background: 'transparent', color: t.text, fontFamily: t.mono, fontSize: t.textXs, fontWeight: '500', overflowWrap: 'anywhere' },
});
const waitCss = css({
  marginTop: 'auto',
  padding: '10px 12px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  color: t.textMuted,
  fontSize: t.textXs,
  lineHeight: '1.5',
  '&[data-block="true"]': { borderColor: t.warning, color: t.text, background: t.warningSoft },
});
const footCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: t.s4,
  padding: `10px ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  color: t.textFaint,
  fontSize: t.textXs,
  '& > span': { display: 'flex', gap: '6px', alignItems: 'center' },
  '& .pfx': { fontFamily: t.mono, fontWeight: '500', color: t.textMuted },
});

/** The icon an option without an avatar shows. */
function iconOf(it: PaletteItem): IconNode {
  if (it.id === 'assign:me') return User;
  if (it.op?.kind === 'scan') return Shield;
  if (it.op?.kind === 'ask') return MessageCircle;
  if (it.href === routes.team.href()) return Users;
  if (it.href === routes.tasks.href()) return ClipboardList;
  if (it.href === routes.needsYou.href()) return Inbox;
  if (it.group === 'Chats') return MessagesSquare;
  if (it.group === 'Tasks') return ClipboardList;
  if (it.group === 'Agents') return Bot;
  if (it.group === 'Actions') return Zap;
  return ArrowRight;
}

/** Typing in a field: single-letter shortcuts stay out of it. */
const typing = (target: EventTarget | null): boolean => {
  const el = target as HTMLElement | null;
  return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName ?? ''));
};

export function CommandPalette(handle: Handle<{ onMessage: (agentId: string) => void; onNavigate?: (href: string) => void; taskId?: string }>) {
  const s = services(handle);
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href));
  watch(handle, s.ui);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const act = action(handle);
  let dialog: HTMLDialogElement | undefined;
  let input: HTMLInputElement | undefined;
  let query = '';
  let active = 0;
  let pressedBackdrop = false; // see ui/dialog.tsx: a drag from the input out to the backdrop is not a click on it
  const open = () => s.ui.paletteOpen;
  const ctxId = () => (open() ? handle.props.taskId : undefined);
  const inList = (id: string | undefined) => (id ? s.project.taskList.find((x) => x.id === id) : undefined);

  // recent chats, read while the menu is open
  const chats = liveQuery(handle, {
    load: () => s.client.chatList({ limit: PALETTE_CHATS }),
    when: (e) => e.type === 'chat.created' || e.type === 'chat.updated' || e.type === 'chat.deleted',
    enabled: open,
    debounceMs: 300,
  });
  // what the options about the task in context need, read while the menu is open over a task's page
  const ctxTask = liveQuery(handle, {
    load: async (): Promise<Task> => (await s.client.taskGet(handle.props.taskId!)).task,
    when: (e) => e.taskId === handle.props.taskId,
    key: () => handle.props.taskId,
    enabled: () => !!ctxId() && !inList(ctxId()),
  });
  const local = liveQuery(handle, {
    load: async (): Promise<{ ids: string[]; providerId: string }> => {
      try {
        const v = await s.client.localGet();
        return { ids: (v.usedBy ?? []).map((u) => u.agentId), providerId: v.settings?.providerId ?? '' };
      } catch {
        return { ids: [], providerId: '' }; // no models on this PC (another system, an older core): no local helper
      }
    },
    when: (e) => e.type === 'local.changed',
    enabled: () => !!ctxId(),
  });
  const adapters = liveQuery(handle, {
    load: () => s.client.adapterList().catch(() => []),
    when: (e) => e.type.startsWith('connection.') || e.type.startsWith('adapter.'),
    enabled: () => !!ctxId(),
  });
  const conns = liveQuery(handle, {
    load: (): Promise<Connection[]> => s.client.connectionList().catch(() => []),
    when: (e) => e.type.startsWith('connection.'),
    enabled: () => !!ctxId(),
  });
  const runMode = liveQuery(handle, {
    load: (): Promise<ProjectRunMode> => s.client.projectRunMode(s.workspace.projectId!).catch(() => ({ runMode: 'native' })),
    when: (e) => e.type.startsWith('project.'),
    key: () => s.workspace.projectId,
    enabled: () => !!ctxId() && !!s.workspace.projectId,
  });
  // the core's preview of the highlighted assignment
  let previewFor: { id: string; assignee: string } | undefined;
  const preview = liveQuery(handle, {
    load: () => s.client.taskAssignPreview(previewFor!.id, previewFor!.assignee),
    when: (e) => e.taskId === previewFor?.id || e.type.startsWith('agent.'),
    key: () => previewFor,
    enabled: () => !!previewFor,
    debounceMs: 300,
  });

  // Ctrl+K (or the person's binding, decision P132) toggles the menu anywhere but inside the terminal (kill to end of line) or
  // the diff editor (a chord). G then I, T or C (within a second) goes to Inbox, Tasks or Crew, never while typing in a field.
  let gAt = 0;
  window.addEventListener(
    'keydown',
    (e) => {
      if (ownsKeys(e.target)) return;
      if (shortcutOf(e) === 'palette') {
        e.preventDefault();
        s.ui.setPalette(!s.ui.paletteOpen);
        return;
      }
      if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || s.ui.paletteOpen || typing(e.target) || e.repeat) return;
      const k = e.key.toLowerCase();
      const href = GO_CHORDS[k];
      if (gAt && Date.now() - gAt <= 1000 && href) {
        gAt = 0;
        e.preventDefault();
        e.stopImmediatePropagation(); // the second key is the chord's, not a page's own shortcut
        go(href);
        return;
      }
      gAt = k === 'g' ? Date.now() : 0;
    },
    { signal: handle.signal },
  );

  const sync = () => {
    if (!dialog) return;
    if (s.ui.paletteOpen && !dialog.open) {
      query = '';
      active = 0;
      if (input) input.value = '';
      if (act.error) act.clear();
      dialog.showModal();
      input?.focus();
      void handle.update();
    } else if (!s.ui.paletteOpen && dialog.open) dialog.close();
  };

  // the context of the last render: what the options about the task were built from
  let ctx: TaskContext | undefined;
  let askChoice: ChatChoice | undefined;
  let projectKey: string | undefined;

  const runOp = async (it: PaletteItem, andOpen: boolean) => {
    const op = it.op;
    const c = ctx;
    if (!op || !c || act.busy) return;
    const task = c.task;
    const key = taskKey(task.seq, projectKey);
    const href = await act.run(it.id, async (): Promise<string> => {
      switch (op.kind) {
        case 'assign':
          await s.client.taskAssign(task.id, op.assignee);
          return routes.task.href({ id: task.id });
        case 'split':
          await s.client.leadSplit(task.id);
          return routes.task.href({ id: task.id });
        case 'scan':
          await s.client.securityScanStart(task.projectId);
          return routes.security.href();
        case 'ask': {
          const choice = askChoice;
          if (!choice) throw new Error('No agent CLI can start a chat');
          const chat = await s.client.chatCreate({ projectId: task.projectId, adapter: choice.adapter.id, ...(choice.wslDistro ? { wslDistro: choice.wslDistro } : {}), title: `${key} ${task.title}` });
          const desc = task.description.trim();
          await s.client.chatSend(chat.id, `About ${key} "${task.title}"${desc ? `:\n\n${desc}` : ''}`);
          return routes.chat.href({ id: chat.id });
        }
      }
    });
    if (href === undefined) return; // the error shows in the panel
    s.ui.setPalette(false);
    if (andOpen || op.kind === 'ask') go(href);
  };

  const run = (it: PaletteItem, andOpen = false) => {
    if (it.op) {
      void runOp(it, andOpen);
      return;
    }
    s.ui.setPalette(false);
    if (it.href) {
      go(it.href);
      return;
    }
    const [kind, arg] = (it.action ?? '').split(':') as [string, string];
    switch (kind) {
      case 'inspect':
        s.ui.openInspector(arg);
        break;
      case 'dm':
        handle.props.onMessage(arg);
        break;
      case 'dock':
        if (arg === 'toggle') s.ui.toggleDock();
        else s.ui.openDock(arg as DockTab);
        break;
      case 'details':
        s.ui.setDetails(!s.ui.detailsOpen);
        break;
      case 'theme':
        s.ui.setTheme(arg as ThemeChoice);
        break;
      case 'chat':
        // the Chats screen opens the dialog over the chat that is open (no URL flag: Back and a reload never reopen it)
        s.ui.requestNewChat();
        if (!isChatsPath(window.location.pathname)) go(routes.chats.href());
        break;
    }
  };

  /** Where an agent's CLI runs, in the Connections page's words: its own run mode, else the project's (the core's rule). */
  const runtimeOf = (a: Pick<Agent, 'adapter' | 'runMode' | 'wslDistro'>): string => {
    const all = conns.data ?? [];
    if (a.runMode === 'wsl') return cliName({ adapter: a.adapter, runMode: 'wsl', distro: a.wslDistro }, all);
    if (a.runMode === 'native') return cliName({ adapter: a.adapter, runMode: 'native' }, all);
    const p = runMode.data;
    const canWSL = (adapters.data ?? []).find((x) => x.id === a.adapter)?.capabilities?.runModes?.includes('wsl') ?? false;
    return p?.runMode === 'wsl' && canWSL ? cliName({ adapter: a.adapter, runMode: 'wsl', distro: p.wslDistro }, all) : cliName({ adapter: a.adapter, runMode: 'native' }, all);
  };
  const previewRuntime = (a: PreviewAgent): string => cliName({ adapter: a.adapter, runMode: a.runMode, distro: a.wslDistro }, conns.data ?? []);

  const what = (it: PaletteItem | undefined) => {
    if (act.error) return <ErrorNote error={act.error} what={`Could not ${it ? it.label.charAt(0).toLowerCase() + it.label.slice(1) : 'run it'}`} />;
    if (!it) return null;
    if (act.busy === it.id) return <p>{it.op?.kind === 'split' ? 'Asking the team lead...' : 'Working on it...'}</p>;
    if (it.op?.kind !== 'assign') return <p>{it.what ?? it.label}</p>;
    const p: AssignPreview | undefined = preview.data;
    if (preview.error) return <ErrorNote error={preview.error} what="Could not tell what would happen" />;
    if (!p || p.assignee !== it.op.assignee || p.taskId !== ctx?.task.id) return <p>Reading what would happen...</p>;
    const steps = previewSteps(p);
    const block = previewBlock(p);
    const wait = block || previewWait(p, projectKey);
    const human = s.workspace.info.data?.user || p.assignee.replace(/^human:/, '');
    return [
      <div mix={whoCss} data-testid="palette-who">
        {p.agent ? <Avatar id={p.agent.id} name={p.agent.name} size={32} kind="agent" /> : <Avatar id={p.assignee} name={human} size={32} kind="human" />}
        <div class="who">
          <span class="name">{p.agent ? p.agent.name : 'You'}</span>
          <span class="sub">{p.agent ? [previewRuntime(p.agent), p.agent.model || 'default model', (p.agent.presence || p.agent.status || '').toLowerCase()].filter(Boolean).join(' · ') : 'No agent works on it'}</span>
        </div>
      </div>,
      <ol mix={stepsCss} data-testid="palette-steps">
        {steps.map((st, i) => (
          <li key={i}>
            {st.text}
            {st.code ? <code>{st.code}</code> : null}
          </li>
        ))}
      </ol>,
      wait ? (
        <div mix={waitCss} data-testid="palette-wait" data-block={block ? 'true' : undefined}>
          {wait}
        </div>
      ) : null,
    ];
  };

  return () => {
    handle.queueTask(sync);
    const p = s.project;
    const ws = s.workspace;
    const isOpen = s.ui.paletteOpen;
    const taskId = isOpen ? handle.props.taskId : undefined;
    const task = taskId ? (inList(taskId) ?? (ctxTask.data?.id === taskId ? ctxTask.data : undefined)) : undefined;
    const project = task ? (ws.projectList.find((x) => x.id === task.projectId) ?? ws.project) : ws.project;
    projectKey = project?.key || undefined;
    // the options about the task, once the task is known; the reads they name (CLI, local models) fill in as they arrive
    if (task && project) {
      const choices = chatChoices(adapters.data ?? [], conns.data ?? []);
      askChoice = pickChatChoice(choices, '');
      ctx = {
        task,
        projectName: project.name,
        hasRepo: projectHasRepo(project),
        human: ws.info.data?.human || undefined,
        localIds: new Set([...(local.data?.ids ?? []), ...p.agentList.filter((a) => !!local.data?.providerId && a.providerId === local.data.providerId).map((a) => a.id)]),
        runtime: runtimeOf,
        askCli: askChoice ? (askChoice.wslDistro ? cliName({ adapter: askChoice.adapter.id, runMode: 'wsl', distro: askChoice.wslDistro }, conns.data ?? []) : cliName({ adapter: askChoice.adapter.id, runMode: 'native' })) : undefined,
      };
    } else {
      ctx = undefined;
      askChoice = undefined;
    }
    const all = isOpen ? buildItems(p.roomList, p.taskList, p.agentList, chats.data ?? [], { projectKey, context: ctx }) : [];
    const shown = filterItems(all, query);
    const groups = arrange(shown, query);
    const flat = groups.flatMap((g) => g.items);
    const idx = Math.min(active, Math.max(flat.length - 1, 0));
    const current = flat[idx];
    previewFor = current?.op?.kind === 'assign' && ctx ? { id: ctx.task.id, assignee: current.op.assignee } : undefined;
    const listId = `palette-list-${handle.id}`;
    const optId = (i: number) => `${listId}-${i}`;
    let n = -1;
    const option = (it: PaletteItem) => {
      n++;
      const i = n;
      return (
        <div
          key={it.id}
          id={optId(i)}
          role="option"
          aria-selected={i === idx}
          data-testid="palette-option"
          mix={[
            optCss,
            on('mousemove', () => {
              if (active !== i) {
                active = i;
                void handle.update();
              }
            }),
            on('mousedown', (e) => {
              e.preventDefault();
              active = i;
              run(it, e.ctrlKey || e.metaKey);
            }),
          ]}
        >
          {it.agent ? (
            <Avatar id={it.agent.id} name={it.agent.name} size={20} kind="agent" />
          ) : (
            <span class="ico">
              <Icon icon={iconOf(it)} size={16} />
            </span>
          )}
          <span class="lbl">{it.label}</span>
          {it.hint ? <span class="hint">{it.hint}</span> : null}
          {(it.keys ?? []).map((k) => (
            <kbd>{k}</kbd>
          ))}
          <kbd class="enter" aria-hidden="true">
            ↵
          </kbd>
        </div>
      );
    };
    const key = task ? taskKey(task.seq, projectKey) : '';
    return (
      <dialog
        aria-label="Command menu"
        mix={[
          dialogCss,
          ref((d) => {
            dialog = d;
            sync();
          }),
          on('cancel', (e) => {
            e.preventDefault();
            s.ui.setPalette(false);
          }),
          on('close', () => {
            if (s.ui.paletteOpen) s.ui.setPalette(false);
          }),
          on('pointerdown', (e) => {
            pressedBackdrop = e.target === e.currentTarget;
          }),
          on('click', (e) => {
            const closing = pressedBackdrop && e.target === e.currentTarget;
            pressedBackdrop = false;
            if (closing) s.ui.setPalette(false);
          }),
        ]}
      >
        <div mix={headCss}>
          {task ? (
            <span mix={chipCss} data-testid="palette-context" title={`The menu acts on ${key} ${task.title}`}>
              <span class="key">{key}</span>
              <span class="title">{task.title}</span>
            </span>
          ) : (
            <Icon icon={Search} size={18} />
          )}
          <input
            type="text"
            placeholder="Go to, open or do..."
            aria-label="Command"
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={flat.length ? optId(idx) : undefined}
            aria-autocomplete="list"
            aria-describedby={task ? `${listId}-ctx` : undefined}
            autocomplete="off"
            spellcheck={false}
            mix={[
              ref((n) => (input = n)),
              on('input', (e) => {
                query = e.currentTarget.value;
                active = 0;
                if (act.error) act.clear();
                void handle.update();
              }),
              on('keydown', (e) => {
                if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                  e.preventDefault();
                  if (!flat.length) return;
                  active = (idx + (e.key === 'ArrowDown' ? 1 : -1) + flat.length) % flat.length;
                  if (act.error) act.clear();
                  void handle.update().then(() => document.getElementById(optId(active))?.scrollIntoView({ block: 'nearest' }));
                } else if (e.key === 'Enter') {
                  e.preventDefault();
                  if (current) run(current, e.ctrlKey || e.metaKey);
                }
              }),
            ]}
          />
          {task ? (
            <span id={`${listId}-ctx`} hidden>
              {`Acts on ${key} ${task.title}`}
            </span>
          ) : null}
          <kbd aria-hidden="true">esc</kbd>
        </div>
        <div mix={bodyCss}>
          <div id={listId} role="listbox" aria-label="Commands" mix={listCss}>
            {flat.length === 0 && isOpen ? (
              <div role="presentation" mix={emptyCss}>
                Nothing matches.{p.taskCoverage.partial ? ` ${taskSearchNote(p.taskCoverage)}` : ''}
              </div>
            ) : null}
            {groups.map((g) => (
              <div key={`g-${g.group}`} role="group" aria-labelledby={`${listId}-g-${g.group.replace(/\s/g, '')}`}>
                <div id={`${listId}-g-${g.group.replace(/\s/g, '')}`} role="presentation" mix={groupCss}>
                  {g.group}
                </div>
                {g.items.map(option)}
              </div>
            ))}
          </div>
          <aside aria-label="What happens" mix={asideCss} data-testid="palette-what">
            <span mix={groupCss} style={{ padding: '0' }}>
              What happens
            </span>
            {isOpen ? what(current) : null}
          </aside>
        </div>
        <div mix={footCss}>
          <span>
            <kbd>↑</kbd>
            <kbd>↓</kbd> choose
          </span>
          <span>
            <kbd>↵</kbd> run
          </span>
          <span>
            <kbd>Ctrl</kbd>
            <kbd>↵</kbd> run and open
          </span>
          <span style={{ marginLeft: 'auto' }}>
            Type <span class="pfx">&gt;</span> for settings, <span class="pfx">#</span> for tasks, <span class="pfx">@</span> for agents
          </span>
        </div>
      </dialog>
    );
  };
}

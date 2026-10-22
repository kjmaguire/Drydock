// The Inbox (graphite, decision P97): what needs the person in every project, what runs now and what was done today, with the
// selected item's detail beside the list. A request is answered from its detail with what the core says of it (approval.get):
// a worker's tool permission request is approved for the rest of its task when the core allows that, else once, or denied; a
// plain approval approved or denied; a spec gate accepted as written and a review merged. Everything else (a budget pause, a
// CI gate, a question to answer, an outbound write) opens where it is decided. J and K move the selection, A approves, D denies,
// E marks a failed check, a watchdog pause or a question done, Enter opens the item's page. The page is the workspace's: it
// opens with or without a project, and the sidebar's Inbox and Running counts are the same lists.

import { css, navigate, on, type Handle } from 'remix/component';
import { CircleCheck, CircleHelp, CirclePause, FileText, GitMerge, ListTree, MessageSquare, OctagonX, Shield, Eye, type IconNode } from 'lucide';
import type { ApprovalDetail } from '../../api/inboxClient.ts';
import type { RunningScan } from '../../api/securityTypes.ts';
import type { NeedsYouItem, Task, TaskSummary } from '../../api/types.ts';
import { plural } from '../../lib/format.ts';
import { ownsKeys } from '../../lib/keys.ts';
import { visible } from '../../lib/visible.ts';
import { routes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { starts } from '../../state/live.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import type { InboxTab } from '../../state/ui.ts';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { headerCss } from '../../ui/layout.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { adapterLabel } from '../shared/cli-name.ts';
import { criterionLine, specPayload } from '../tasks/tasks.ts';
import {
  agoWords,
  answerOf,
  canAck,
  clock,
  doingWords,
  LOOK_WORDS,
  msOf,
  needsHint,
  needsLook,
  needsRowTitle,
  needsSubtitle,
  needsTarget,
  scanProgress,
  shortHash,
  startOfToday,
  stepFraction,
  taskKey,
  untilWords,
  type Answer,
  type NeedsLook,
} from './needs-you.ts';

const LOOK: Record<NeedsLook, { icon: IconNode; tone: Tone }> = {
  approval: { icon: Shield, tone: 'warn' },
  spec: { icon: FileText, tone: 'accent' },
  merge: { icon: GitMerge, tone: 'ok' },
  plan: { icon: ListTree, tone: 'accent' },
  review: { icon: Eye, tone: 'accent' },
  question: { icon: CircleHelp, tone: 'accent' },
  failed_check: { icon: OctagonX, tone: 'err' },
  watchdog: { icon: CirclePause, tone: 'warn' },
  chat: { icon: MessageSquare, tone: 'warn' },
};

// the page: the list on the left and the selected item beside it; a narrow page puts the item under the list
const frameCss = css({ height: '100%', minHeight: '0', containerType: 'inline-size' });
const gridCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 3fr) minmax(0, 2fr)',
  height: '100%',
  minHeight: '0',
  '@container (max-width: 820px)': { display: 'block', height: 'auto', minHeight: '100%', overflowY: 'auto' },
});
const listSide = css({
  display: 'flex',
  flexDirection: 'column',
  minWidth: '0',
  minHeight: '0',
  borderRight: `1px solid ${t.border}`,
  '@container (max-width: 820px)': { borderRight: '0', borderBottom: `1px solid ${t.border}` },
});
const hintCss = css({ display: 'flex', alignItems: 'center', gap: '6px', marginLeft: 'auto', color: t.textFaint, fontSize: t.textXs });
const scrollCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', paddingBottom: t.s3, '@container (max-width: 820px)': { overflowY: 'visible' } });
const groupCss = css({ margin: '0', padding: `${t.s3} 20px 6px`, color: t.textFaint, fontSize: t.textXs, fontWeight: '500' });
const listCss = css({ display: 'grid', margin: '0', padding: '0', listStyle: 'none' });
const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  width: '100%',
  minHeight: '44px',
  padding: '10px 20px',
  border: '0',
  borderRadius: '0',
  background: 'transparent',
  color: t.text,
  fontSize: t.textMd,
  fontWeight: '400',
  textAlign: 'left',
  justifyContent: 'flex-start',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-current="true"]': { background: t.bgActive, boxShadow: `inset 2px 0 0 ${t.accent}` },
  '&:focus-visible': { outline: 'none', boxShadow: `inset 0 0 0 2px ${t.accent}` },
  '& .text': { display: 'flex', flexDirection: 'column', gap: '2px', flex: '1', minWidth: '0' },
  '& .title': { fontWeight: '500', overflowWrap: 'anywhere' },
  '& .sub': { color: t.textFaint, fontSize: t.textXs },
});
const runRowCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1fr) 120px 64px',
  alignItems: 'center',
  gap: t.s3,
  width: '100%',
  minHeight: '36px',
  padding: '9px 20px',
  border: '0',
  borderRadius: '0',
  background: 'transparent',
  color: t.text,
  fontSize: t.textMd,
  fontWeight: '400',
  textAlign: 'left',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-current="true"]': { background: t.bgActive, boxShadow: `inset 2px 0 0 ${t.accent}` },
  '&:focus-visible': { outline: 'none', boxShadow: `inset 0 0 0 2px ${t.accent}` },
  '& .what': { display: 'flex', alignItems: 'center', gap: '10px', minWidth: '0' },
  '& .line': { whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .time': { color: t.textFaint, fontFamily: t.mono, fontSize: t.textXs, textAlign: 'right', fontVariantNumeric: 'tabular-nums' },
});
const keyCss = css({ color: t.textFaint, fontFamily: t.mono, fontSize: t.textXs, marginRight: '6px' });
const tileCss = css({ display: 'inline-grid', placeItems: 'center', flex: 'none', width: '22px', height: '22px', borderRadius: '6px' });
const dotCss = css({ display: 'inline-block', flex: 'none', width: '8px', height: '8px', borderRadius: '50%' });
const barCss = css({ height: '4px', borderRadius: '2px', background: t.bgActive, overflow: 'hidden', '& > span': { display: 'block', height: '4px', borderRadius: '2px' } });
const emptyCss = css({ margin: '0', padding: `6px 20px ${t.s2}`, color: t.textMuted });
const footCss = css({ display: 'flex', flexWrap: 'wrap', gap: t.s4, flex: 'none', padding: '12px 20px', borderTop: `1px solid ${t.border}`, color: t.textFaint, fontSize: t.textXs, '& > span': { display: 'inline-flex', alignItems: 'center', gap: '6px' } });

// 8 px of the pane's 24 px inset is the pane's own, so its text never runs up against the list's (the density budget's 8 px)
const asideCss = css({
  display: 'flex',
  flexDirection: 'column',
  minWidth: '0',
  minHeight: '0',
  overflowY: 'auto',
  paddingLeft: '8px',
  background: t.bgSidebar,
  '@container (max-width: 820px)': { paddingLeft: '4px' },
});
const paneHead = css({ display: 'flex', flexDirection: 'column', gap: '10px', padding: '18px 24px 18px 16px', borderBottom: `1px solid ${t.border}`, '& h2': { margin: '0', fontSize: t.textXl, fontWeight: '600', lineHeight: '1.35', overflowWrap: 'anywhere' }, '& p': { margin: '0', color: t.textMuted, lineHeight: '1.55' } });
const kindLine = css({ fontSize: t.textXs, fontWeight: '500' });
const dlCss = css({
  display: 'grid',
  gridTemplateColumns: '120px minmax(0, 1fr)',
  gap: `10px ${t.s3}`,
  margin: '0',
  padding: '16px 24px 16px 16px',
  borderBottom: `1px solid ${t.border}`,
  '& dt': { color: t.textFaint },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere' },
  '& dd.mono': { fontFamily: t.mono, fontSize: t.textXs },
  '& dd code': { fontSize: t.textXs, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  '& ol': { margin: '0', paddingLeft: '18px', display: 'grid', gap: '4px' },
});
const staysCss = css({ display: 'flex', flexDirection: 'column', gap: t.s2, padding: '16px 24px 16px 16px', '& .h': { color: t.textFaint, fontSize: t.textXs, fontWeight: '500' }, '& .b': { color: t.textMuted } });
const actionsCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: '10px',
  marginTop: 'auto',
  padding: '16px 24px 16px 16px',
  borderTop: `1px solid ${t.border}`,
  '& kbd': { marginLeft: '2px' },
  '& [data-variant="primary"] kbd': { background: 'transparent', borderColor: 'currentColor', color: 'inherit', opacity: '0.7' },
  '& .end': { marginLeft: 'auto' },
});
const openRow = css({ display: 'flex', margin: '0 0 0 -10px' });
const quietCss = css({ margin: '0', padding: '0 24px 12px 16px', color: t.textFaint, fontSize: t.textXs });
const statusCss = css({ margin: '0', padding: '12px 24px 0 16px', color: t.textMuted });
const noneCss = css({ margin: 'auto', padding: t.s6, color: t.textMuted, textAlign: 'center' });

const TABS: InboxTab[] = ['needs', 'running', 'done'];
const tabOf = (v: string | undefined): InboxTab | undefined => (TABS.includes(v as InboxTab) ? (v as InboxTab) : undefined);

/** One row of the list: an item that needs you, a running task or scan, a task done today. Keys are unique across them. */
type Row =
  | { key: string; type: 'needs'; item: NeedsYouItem }
  | { key: string; type: 'task'; summary: TaskSummary }
  | { key: string; type: 'scan'; scan: RunningScan }
  | { key: string; type: 'done'; task: Task };

/** Whether a key press is typing (a field, a box, an editor), so a single-letter shortcut must leave it alone. */
function typing(e: KeyboardEvent): boolean {
  if (e.ctrlKey || e.metaKey || e.altKey || ownsKeys(e.target)) return true;
  const el = e.target instanceof HTMLElement ? e.target : null;
  return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || !!el.closest?.('dialog[open], [role="combobox"], [role="listbox"], [role="menu"]'));
}

export function NeedsYouScreen(handle: Handle<{ tab?: string; /** Where opening goes (tests); default the router's navigate. */ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.ui);
  const done = query(handle, {
    load: () => s.client.taskList('', ['COMPLETED'], { completedSince: startOfToday() }),
    when: (e) => starts(e, 'task.'),
    key: () => startOfToday(),
  });
  const ack = action(handle);
  let selected: string | undefined;
  let lastIndex = 0;
  // the address's tab, once it has been applied (null: not yet, so the first render applies it, ?tab= or none)
  let propTab: string | undefined | null = null;
  let now = Date.now();
  let rows: Row[] = [];
  /** What the selected request's detail can answer here (its A key shows on the row when it can). */
  let answered: { key: string; kind: Answer['kind'] } | undefined;
  const onAnswer = (key: string, kind: Answer['kind']) => {
    if (answered?.key === key && answered.kind === kind) return;
    answered = { key, kind };
    void handle.update();
  };

  // the clocks of what runs and the "asked 2 min ago" words move on
  const timer = setInterval(() => {
    now = Date.now();
    void handle.update();
  }, 1000);
  handle.signal.addEventListener('abort', () => clearInterval(timer));

  const tab = (): InboxTab => s.ui.inboxTab;
  const setTab = (name: string) => {
    const next = tabOf(name) ?? 'needs';
    if (next === tab()) return;
    selected = undefined;
    s.ui.setInboxTab(next);
  };

  const project = (id: string | undefined) => s.workspace.projectList.find((p) => p.id === id);
  const summaryOf = (taskId: string | undefined) => (taskId ? s.workspace.summaries.data?.summaries.find((x) => x.taskId === taskId) : undefined);
  const keyOfTask = (projectId: string | undefined, seq: number | undefined) => taskKey(s.workspace.projectKey(projectId), seq);

  const hrefOf = (r: Row): string => {
    switch (r.type) {
      case 'needs':
        return needsTarget(r.item);
      case 'task':
        return routes.task.href({ id: r.summary.taskId });
      case 'done':
        return routes.task.href({ id: r.task.id });
      case 'scan':
        return routes.securityScan.href();
    }
  };
  const projectOf = (r: Row): string | undefined =>
    r.type === 'needs' ? r.item.projectId : r.type === 'task' ? r.summary.projectId : r.type === 'done' ? r.task.projectId : r.scan.projectId;
  /** Opens the row's page, in its own project (a task page and a project's Integrations page are of the open project). */
  const open = (r: Row) => {
    const pid = projectOf(r);
    if (pid && pid !== s.workspace.projectId) s.workspace.selectProject(pid);
    const href = hrefOf(r);
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href);
  };
  const selectRow = (key: string) => {
    selected = key;
    void handle.update();
  };
  const current = (): Row | undefined => rows.find((r) => r.key === selected);

  const markDone = (it: NeedsYouItem) => {
    if (!canAck(it)) return;
    void ack.run('ack', async () => {
      await s.client.needsYouAck(it.key);
      await s.workspace.needs.reload();
    });
  };

  window.addEventListener(
    'keydown',
    (e) => {
      if (typing(e)) return;
      const k = e.key.length === 1 ? e.key.toLowerCase() : e.key;
      if (k === 'j' || k === 'k') {
        if (!rows.length) return;
        e.preventDefault();
        const i = rows.findIndex((r) => r.key === selected);
        const n = i < 0 ? 0 : k === 'j' ? Math.min(rows.length - 1, i + 1) : Math.max(0, i - 1);
        selectRow(rows[n]!.key);
        handle.queueTask(() => document.querySelector<HTMLElement>(`[data-row-key="${CSS.escape(rows[n]!.key)}"]`)?.scrollIntoView({ block: 'nearest' }));
      } else if (k === 'e') {
        const r = current();
        if (r?.type !== 'needs' || !canAck(r.item)) return;
        e.preventDefault();
        markDone(r.item);
      } else if (k === 'Enter') {
        const el = e.target instanceof HTMLElement ? e.target : null;
        // Enter on another control (a button of the detail, a link) is that control's
        if (el && el !== document.body && !el.closest('[data-row-key]') && el.closest('button, a, [role="tab"]')) return;
        const r = current();
        if (!r) return;
        e.preventDefault();
        open(r);
      }
    },
    { signal: handle.signal },
  );

  return () => {
    const ws = s.workspace;
    // the sidebar's Running link and a link with ?tab= choose the tab when the address changes
    if (handle.props.tab !== propTab) {
      propTab = handle.props.tab;
      const want = tabOf(propTab) ?? 'needs';
      if (want !== tab()) handle.queueTask(() => s.ui.setInboxTab(want));
    }
    const active = tab();
    const needs = ws.needs.data ?? [];
    const runningTasks = ws.runningTasks;
    const scans = ws.runningScans;
    const doneList = done.data ?? [];

    const needsRows: Row[] = needs.map((item) => ({ key: `needs:${item.key}`, type: 'needs', item }));
    const runRows: Row[] = [
      ...runningTasks.map((summary): Row => ({ key: `task:${summary.taskId}`, type: 'task', summary })),
      ...scans.map((scan): Row => ({ key: `scan:${scan.id}`, type: 'scan', scan })),
    ];
    const doneRows: Row[] = doneList.map((task) => ({ key: `done:${task.id}`, type: 'done', task }));
    rows = active === 'needs' ? [...needsRows, ...runRows] : active === 'running' ? runRows : doneRows;
    // the selection stays on its item; when the item leaves (it was answered), the one now in its place is selected
    const at = rows.findIndex((r) => r.key === selected);
    if (at >= 0) lastIndex = at;
    else if (rows.length) selected = rows[Math.min(lastIndex, rows.length - 1)]!.key;
    else selected = undefined;
    const sel = current();

    const running = runRows.length;
    const items = [
      { name: 'needs', label: <>Needs you <span style={{ color: needs.length ? t.warning : undefined }} data-testid="tab-count">{needs.length}</span></>, testId: 'inbox-tab-needs' },
      { name: 'running', label: <>Running <span data-testid="tab-count">{running}</span></>, testId: 'inbox-tab-running' },
      { name: 'done', label: <>Done today <span data-testid="tab-count">{doneList.length}</span></>, testId: 'inbox-tab-done' },
    ];

    const needsRow = (r: Extract<Row, { type: 'needs' }>) => {
      const it = r.item;
      const look = needsLook(it);
      const v = LOOK[look];
      const p = project(it.projectId);
      const summary = summaryOf(it.taskId);
      const key = keyOfTask(it.projectId, it.taskSeq);
      const isSel = r.key === selected;
      return (
        <li key={r.key} data-testid="needs-item" data-kind={it.kind} data-look={look} data-project={it.projectId}>
          <button type="button" mix={[rowCss, on('click', () => selectRow(r.key))]} data-row-key={r.key} aria-current={isSel ? 'true' : undefined}>
            <span mix={tileCss} aria-hidden="true" style={{ background: tone[v.tone].bg, color: tone[v.tone].fg }}>
              <Icon icon={v.icon} size={13} />
            </span>
            <span class="text">
              <span class="title">
                {key ? <span mix={keyCss} data-testid="task-key">{key}</span> : null}
                {visible(needsRowTitle(it, summary))}
              </span>
              <span class="sub" data-testid="needs-sub">{needsSubtitle(it, p?.name, summary, now)}</span>
            </span>
            {isSel && answered?.key === r.key && answered.kind !== 'open' ? <kbd aria-hidden="true">A</kbd> : null}
          </button>
        </li>
      );
    };
    const progress = (frac: number | undefined, fill: string) => (
      <span mix={barCss} role="progressbar" aria-label="Progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={frac === undefined ? undefined : Math.round(frac * 100)}>
        {frac === undefined ? null : <span style={{ width: `${Math.round(frac * 100)}%`, background: fill }} />}
      </span>
    );
    const runRow = (r: Extract<Row, { type: 'task' | 'scan' }>) => {
      const isSel = r.key === selected;
      if (r.type === 'scan') {
        const sc = r.scan;
        const pr = scanProgress(sc);
        return (
          <li key={r.key} data-testid="running-scan" data-scan={sc.id}>
            <button type="button" mix={[runRowCss, on('click', () => selectRow(r.key))]} data-row-key={r.key} aria-current={isSel ? 'true' : undefined}>
              <span class="what">
                <span mix={dotCss} aria-hidden="true" style={{ background: t.success }} />
                <span class="line">
                  Security scan of {sc.name}
                  {pr.engine ? ` · ${pr.engine}` : ''}
                </span>
              </span>
              {progress(pr.total ? pr.done / pr.total : undefined, t.success)}
              <span class="time">{clock(now - msOf(sc.startedAt))}</span>
            </button>
          </li>
        );
      }
      const x = r.summary;
      const key = keyOfTask(x.projectId, x.seq) || (x.projectKey ? `${x.projectKey}-${x.seq}` : '');
      const doing = doingWords(x.doing);
      const started = msOf(x.startedAt);
      return (
        <li key={r.key} data-testid="running-item" data-task={x.taskId}>
          <button type="button" mix={[runRowCss, on('click', () => selectRow(r.key))]} data-row-key={r.key} aria-current={isSel ? 'true' : undefined}>
            <span class="what">
              <span mix={dotCss} aria-hidden="true" style={{ background: t.accent }} />
              <span class="line">
                {key ? <span mix={keyCss}>{key}</span> : null}
                {visible(x.title)}
                {doing ? ` · ${visible(doing)}` : ''}
              </span>
            </span>
            {progress(stepFraction(x), t.accent)}
            <span class="time">{started ? clock(now - started) : ''}</span>
          </button>
        </li>
      );
    };
    const doneRow = (r: Extract<Row, { type: 'done' }>) => {
      const tk = r.task;
      const key = keyOfTask(tk.projectId, tk.seq);
      const isSel = r.key === selected;
      return (
        <li key={r.key} data-testid="done-item" data-task={tk.id}>
          <button type="button" mix={[rowCss, on('click', () => selectRow(r.key))]} data-row-key={r.key} aria-current={isSel ? 'true' : undefined}>
            <span mix={tileCss} aria-hidden="true" style={{ background: tone.ok.bg, color: tone.ok.fg }}>
              <Icon icon={CircleCheck} size={13} />
            </span>
            <span class="text">
              <span class="title">
                {key ? <span mix={keyCss}>{key}</span> : null}
                {visible(tk.title)}
              </span>
              <span class="sub">{[project(tk.projectId)?.name, `done ${agoWords(msOf(tk.completed), now)}`].filter(Boolean).join(' · ')}</span>
            </span>
          </button>
        </li>
      );
    };

    const needsSection = (
      <section aria-labelledby="inbox-needs" data-testid="inbox-needs">
        <h2 id="inbox-needs" mix={groupCss}>
          Needs you
        </h2>
        <ErrorNote error={ws.needs.error} what="Could not load what needs you" />
        {ws.needs.loading && !ws.needs.data ? <Loading /> : null}
        {ws.needs.data && needs.length === 0 ? <p mix={emptyCss}>Nothing is waiting for you. Approvals, spec gates, reviews and questions from every project appear here.</p> : null}
        {needs.length ? (
          <ul mix={listCss} aria-label="Items that need you">
            {needsRows.map((r) => needsRow(r as Extract<Row, { type: 'needs' }>))}
          </ul>
        ) : null}
      </section>
    );
    const runningSection = (
      <section aria-labelledby="inbox-running" data-testid="inbox-running">
        <h2 id="inbox-running" mix={groupCss} style={{ paddingTop: active === 'needs' ? '18px' : undefined }}>
          Running
        </h2>
        <ErrorNote error={ws.summaries.error} what="Could not load the running tasks" />
        {ws.summaries.data && running === 0 ? <p mix={emptyCss}>No agent is working on a task and no scan runs.</p> : null}
        {running ? (
          <ul mix={listCss} aria-label="Running now">
            {runRows.map((r) => runRow(r as Extract<Row, { type: 'task' | 'scan' }>))}
          </ul>
        ) : null}
      </section>
    );
    const doneSection = (
      <section aria-labelledby="inbox-done" data-testid="inbox-done">
        <h2 id="inbox-done" mix={groupCss}>
          Done today
        </h2>
        <ErrorNote error={done.error} what="Could not load the tasks done today" />
        {done.loading && !done.data ? <Loading /> : null}
        {done.data && doneList.length === 0 ? <p mix={emptyCss}>No task was completed today yet.</p> : null}
        {doneList.length ? (
          <ul mix={listCss} aria-label="Done today">
            {doneRows.map((r) => doneRow(r as Extract<Row, { type: 'done' }>))}
          </ul>
        ) : null}
      </section>
    );

    return (
      <div mix={frameCss}>
        <div mix={gridCss}>
          <section mix={listSide} aria-labelledby="inbox-title" data-testid="inbox">
            <header mix={headerCss}>
              <h1 id="inbox-title">Inbox</h1>
              <Tabs label="Filter" pills items={items} active={active} onChange={setTab} />
              <span mix={hintCss} aria-label="J and K move the selection">
                <kbd>J</kbd>
                <kbd>K</kbd> move
              </span>
            </header>
            <div mix={scrollCss}>
              <ErrorNote error={ack.error} what="Not marked done" />
              {active === 'needs' ? (
                <>
                  {needsSection}
                  {runningSection}
                </>
              ) : active === 'running' ? (
                runningSection
              ) : (
                doneSection
              )}
            </div>
            <footer mix={footCss} aria-label="Keys">
              <span>
                <kbd>A</kbd> approve
              </span>
              <span>
                <kbd>D</kbd> deny
              </span>
              <span>
                <kbd>{'↵'}</kbd> open
              </span>
              <span>
                <kbd>E</kbd> done
              </span>
            </footer>
          </section>
          <aside mix={asideCss} aria-label="Selected item" data-testid="inbox-detail">
            {!sel ? (
              <p mix={noneCss}>{rows.length ? 'Choose an item to see it here.' : 'Nothing to show here yet.'}</p>
            ) : sel.type === 'needs' ? (
              <NeedsDetail
                key={sel.key}
                item={sel.item}
                summary={summaryOf(sel.item.taskId)}
                taskKey={keyOfTask(sel.item.projectId, sel.item.taskSeq)}
                projectName={project(sel.item.projectId)?.name}
                now={now}
                onOpen={() => open(sel)}
                onDone={() => markDone(sel.item)}
                onAnswer={(kind) => onAnswer(sel.key, kind)}
                acking={ack.busy === 'ack'}
              />
            ) : sel.type === 'task' ? (
              <TaskPane key={sel.key} summary={sel.summary} taskKey={keyOfTask(sel.summary.projectId, sel.summary.seq)} projectName={project(sel.summary.projectId)?.name} now={now} onOpen={() => open(sel)} />
            ) : sel.type === 'scan' ? (
              <ScanPane key={sel.key} scan={sel.scan} now={now} />
            ) : (
              <DonePane key={sel.key} task={sel.task} taskKey={keyOfTask(sel.task.projectId, sel.task.seq)} projectName={project(sel.task.projectId)?.name} now={now} onOpen={() => open(sel)} />
            )}
          </aside>
        </div>
      </div>
    );
  };
}

const lookWords = (look: NeedsLook): string => {
  const w = LOOK_WORDS[look];
  return w.charAt(0).toUpperCase() + w.slice(1);
};

/** The detail of an item that needs you: for a request, what approval.get says of it and the answers the core allows here. */
function NeedsDetail(
  handle: Handle<{
    item: NeedsYouItem;
    summary: TaskSummary | undefined;
    taskKey: string;
    projectName: string | undefined;
    now: number;
    onOpen: () => void;
    onDone: () => void;
    /** Says what this request's detail can answer here, once it is read. */
    onAnswer?: (kind: Answer['kind']) => void;
    acking: boolean;
  }>,
) {
  const s = services(handle);
  watch(handle, s.workspace);
  const detail = query(handle, {
    load: () => s.client.approvalGet(handle.props.item.approvalId!),
    enabled: () => !!handle.props.item.approvalId,
    when: (e) => starts(e, 'approval.', 'run.', 'permission.'),
  });
  const act = action(handle);
  let sent = false;

  const answer = (): Answer => (detail.data ? answerOf(detail.data) : { kind: 'open' });
  const readOnly = () => s.workspace.license.readOnly;
  const respond = (label: string, fn: () => Promise<void>) =>
    void act.run(label, async () => {
      await fn();
      sent = true;
      // the item leaves the list when the core says so; the counts and the list read again now
      await s.workspace.needs.reload();
    });
  const approve = (once = false) => {
    const a = answer();
    const d = detail.data;
    if (!d || a.kind === 'open' || readOnly() || act.busy) return;
    if (a.kind === 'permission' && a.forTask && !once) respond('approve', () => s.client.runApproveForTask(a.runId, d.id));
    else respond(once ? 'once' : 'approve', () => s.client.runRespond(a.runId, d.id, 'approved'));
  };
  const deny = () => {
    const a = answer();
    const d = detail.data;
    if (!d || (a.kind !== 'permission' && a.kind !== 'approval') || act.busy) return;
    respond('deny', () => s.client.runRespond(a.runId, d.id, 'rejected'));
  };

  window.addEventListener(
    'keydown',
    (e) => {
      if (typing(e) || e.key.length !== 1) return;
      const k = e.key.toLowerCase();
      if (k === 'a') {
        e.preventDefault();
        approve();
      } else if (k === 'd') {
        e.preventDefault();
        deny();
      }
    },
    { signal: handle.signal },
  );

  return () => {
    const { item: it, summary, taskKey: key, projectName, now, onOpen, onDone, acking } = handle.props;
    const look = needsLook(it);
    const v = LOOK[look];
    const d: ApprovalDetail | undefined = detail.data;
    const a = answer();
    const busy = !!act.busy || sent;
    if (d) handle.queueTask(() => handle.props.onAnswer?.(a.kind));
    const ro = readOnly();
    const title = needsRowTitle(it, summary);
    const description = d?.description || it.detail || '';
    const hint = needsHint(it);
    const agent = d?.agent;
    const ctx = it.context;
    const diff = summary?.diff ?? (ctx?.hasDiff ? { files: ctx.files, insertions: ctx.insertions, deletions: ctx.deletions } : undefined);
    const checksTotal = ctx?.checksTotal ?? summary?.checks.length ?? 0;
    const checksPassed = ctx?.checksPassed ?? summary?.checks.filter((c) => c.status === 'passed').length ?? 0;
    const failing = (summary?.checks ?? []).filter((c) => c.status === 'failed').map((c) => c.kind);
    const spec = d && d.kind === 'review' && d.phase === 'spec' ? specPayload({ id: d.id, nodeId: '', step: 0, spec: { kind: d.kind, title: d.title, payload: d.payload, phase: d.phase } }) : undefined;
    const permission = a.kind === 'permission';
    const review = look === 'spec' || look === 'merge' || look === 'plan' || look === 'review';
    const expires = d?.expiresAt || msOf(it.expiresAt);
    const answerHere = a.kind !== 'open';
    const openLabel = it.chatId ? 'Open the chat' : review ? 'Open to review' : a.kind === 'open' && d ? 'Open to decide' : it.taskId ? 'Open the task' : 'Open';
    const openLink = (
      <a
        href={needsTarget(it)}
        mix={[...buttonStyle(answerHere ? 'ghost' : 'primary', answerHere ? 'sm' : 'md'), on('click', (e) => (e.preventDefault(), onOpen()))]}
        data-testid="detail-open"
      >
        {openLabel}
      </a>
    );

    return (
      <>
        <div mix={paneHead} data-testid="detail-head">
          <span mix={kindLine} style={{ color: tone[v.tone].fg }} data-testid="detail-kind">
            {[lookWords(look), projectName].filter(Boolean).join(' · ')}
          </span>
          <h2>
            {key ? <span mix={keyCss}>{key}</span> : null}
            {visible(title)}
          </h2>
          {description ? <p data-testid="detail-description">{visible(description)}</p> : null}
          {/* what to do where it is decided, for what is not answered here */}
          {(!d || a.kind === 'open') && hint ? <p data-testid="needs-hint">{hint}</p> : null}
          {/* a request answered here keeps its page one step away, under what it is about */}
          {answerHere ? <div mix={openRow}>{openLink}</div> : null}
        </div>
        <ErrorNote error={detail.error} what="Could not read the request" />
        {it.approvalId && detail.loading && !d ? <Loading /> : null}
        <dl mix={dlCss} data-testid="detail-facts">
          {agent || it.agentName ? (
            <>
              <dt>Agent</dt>
              <dd>{agent ? [agent.name || agent.id, agent.adapter ? adapterLabel(agent.adapter) : '', agent.model].filter(Boolean).join(' · ') : it.agentName}</dd>
            </>
          ) : null}
          {d?.permissionMode ? (
            <>
              <dt>Mode</dt>
              <dd>{d.permissionMode.from ? `${d.permissionMode.from} → ${d.permissionMode.to}` : d.permissionMode.to}</dd>
            </>
          ) : null}
          {permission && d?.input ? (
            <>
              <dt>{d.shell ? 'Command' : d.tool || 'Input'}</dt>
              <dd data-testid="detail-input">
                <code>{visible(d.input)}</code>
                {d.shortened ? ' (shortened)' : ''}
              </dd>
            </>
          ) : null}
          {d && it.kind === 'approval' ? (
            <>
              <dt>Applies to</dt>
              <dd data-testid="detail-applies">
                {d.permissionMode ? (agent ? 'This agent only' : 'Agents made from the template') : a.kind === 'permission' && a.forTask ? `This exact call, until ${key || 'the task'} ends` : 'This request only'}
              </dd>
            </>
          ) : null}
          {spec?.criteria?.length ? (
            <>
              <dt>Criteria</dt>
              <dd>
                <ol data-testid="detail-criteria">
                  {spec.criteria.map((c) => (
                    <li key={c.id}>{visible(criterionLine(c))}</li>
                  ))}
                </ol>
              </dd>
            </>
          ) : summary && review && summary.criteria > 0 ? (
            <>
              <dt>Criteria</dt>
              <dd>{plural(summary.criteria, 'acceptance criterion', 'acceptance criteria')}</dd>
            </>
          ) : null}
          {review && diff ? (
            <>
              <dt>Change</dt>
              <dd data-testid="detail-diff">
                <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: '8px', fontVariantNumeric: 'tabular-nums' }}>
                  <span>{plural(diff.files, 'file')}</span> <span style={{ color: t.success }}>+{diff.insertions}</span>{' '}
                  <span style={{ color: t.danger }}>{'−'}{diff.deletions}</span>
                </span>
              </dd>
            </>
          ) : null}
          {review && checksTotal > 0 ? (
            <>
              <dt>Checks</dt>
              <dd data-testid="detail-checks">
                {checksPassed} of {checksTotal} passed{failing.length ? ` · failing: ${failing.join(', ')}` : ''}
              </dd>
            </>
          ) : null}
          {review && ctx?.verdict ? (
            <>
              <dt>Reviewer</dt>
              <dd>{ctx.verdict}</dd>
            </>
          ) : null}
          {d?.worktree ? (
            <>
              <dt>Worktree</dt>
              <dd class="mono">{visible(d.worktree)}</dd>
            </>
          ) : null}
          {expires ? (
            <>
              <dt>Expires</dt>
              <dd>{untilWords(expires, now)}</dd>
            </>
          ) : null}
          {d?.actionHash ? (
            <>
              <dt>Action hash</dt>
              <dd class="mono" title={d.actionHash}>
                {shortHash(d.actionHash)}
              </dd>
            </>
          ) : null}
          {!d ? (
            <>
              <dt>Asked</dt>
              <dd>{agoWords(msOf(it.createdAt), now)}</dd>
            </>
          ) : null}
        </dl>
        {permission ? (
          <div mix={staysCss} data-testid="detail-stays">
            <span class="h">What stays the same</span>
            <span class="b">
              {a.kind === 'permission' && a.forTask
                ? 'Any other call still asks you. Pushes, merges, paths outside the worktree and secrets are asked every time.'
                : 'The next call asks you again.'}
            </span>
          </div>
        ) : null}
        {d && d.status !== 'REQUESTED' ? <p mix={statusCss}>This request was answered already ({d.status.toLowerCase()}).</p> : null}
        <ErrorNote error={act.error} what="Not sent" />
        <ReadOnlyNote />
        {sent ? (
          <p mix={statusCss} role="status">
            Answer sent. It leaves the inbox when the core confirms.
          </p>
        ) : null}
        <div mix={actionsCss} data-testid="detail-actions">
          {a.kind === 'permission' || a.kind === 'approval' ? (
            <>
              <Button variant="primary" data-variant="primary" disabled={busy || ro} mix={[on('click', () => approve())]} data-testid="detail-approve">
                Approve <kbd>A</kbd>
              </Button>
              <Button disabled={busy} mix={[on('click', () => deny())]} data-testid="detail-deny">
                Deny <kbd>D</kbd>
              </Button>
              {a.kind === 'permission' && a.forTask ? (
                <Button variant="ghost" class="end" disabled={busy || ro} mix={[on('click', () => approve(true))]} data-testid="detail-once">
                  Approve once
                </Button>
              ) : null}
            </>
          ) : a.kind === 'spec' || a.kind === 'merge' ? (
            <Button variant="primary" data-variant="primary" disabled={busy || ro} mix={[on('click', () => approve())]} data-testid="detail-approve">
              {a.kind === 'spec' ? 'Accept spec' : 'Merge'} <kbd>A</kbd>
            </Button>
          ) : null}
          {canAck(it) ? (
            <Button disabled={acking} mix={[on('click', onDone)]} data-testid="detail-done">
              Mark done <kbd>E</kbd>
            </Button>
          ) : null}
          {answerHere ? null : openLink}
        </div>
        {a.kind === 'permission' && !a.forTask ? (
          <p mix={quietCss} data-testid="detail-once-note">
            This call can only be approved once{a.note ? `: ${a.note}` : ''}.
          </p>
        ) : null}
      </>
    );
  };
}

/** A running task: where it is and what its agent does. */
function TaskPane(handle: Handle<{ summary: TaskSummary; taskKey: string; projectName: string | undefined; now: number; onOpen: () => void }>) {
  return () => {
    const { summary: x, taskKey: key, projectName, now, onOpen } = handle.props;
    const started = msOf(x.startedAt);
    const passed = x.checks.filter((c) => c.status === 'passed').length;
    return (
      <>
        <div mix={paneHead}>
          <span mix={kindLine} style={{ color: t.accentText }}>
            {['Running', projectName].filter(Boolean).join(' · ')}
          </span>
          <h2>
            {key ? <span mix={keyCss}>{key}</span> : null}
            {visible(x.title)}
          </h2>
          {x.doing ? <p>{visible(x.doing)}</p> : null}
        </div>
        <dl mix={dlCss}>
          <dt>Step</dt>
          <dd>
            {x.stepIndex} of {x.stepCount}
            {x.step ? ` · ${x.step}` : ''}
          </dd>
          {x.maxAttempts ? (
            <>
              <dt>Attempt</dt>
              <dd>
                {x.attempt} of {x.maxAttempts}
              </dd>
            </>
          ) : null}
          {x.checks.length ? (
            <>
              <dt>Checks</dt>
              <dd>
                {passed} of {x.checks.length} passed
              </dd>
            </>
          ) : null}
          {started ? (
            <>
              <dt>Running for</dt>
              <dd class="mono">{clock(now - started)}</dd>
            </>
          ) : null}
        </dl>
        <div mix={actionsCss}>
          <a href={routes.task.href({ id: x.taskId })} mix={[...buttonStyle('primary'), on('click', (e) => (e.preventDefault(), onOpen()))]}>
            Open the task
          </a>
        </div>
      </>
    );
  };
}

/** A security scan in progress: its engines. */
function ScanPane(handle: Handle<{ scan: RunningScan; now: number }>) {
  return () => {
    const { scan, now } = handle.props;
    return (
      <>
        <div mix={paneHead}>
          <span mix={kindLine} style={{ color: t.success }}>
            Security scan
          </span>
          <h2>{visible(scan.name)}</h2>
          <p>Only files are read. Nothing in the folder runs.</p>
        </div>
        <dl mix={dlCss}>
          {scan.engines.flatMap((e) => [<dt key={`${e.id}-t`}>{e.name}</dt>, <dd key={`${e.id}-d`}>{e.status}</dd>])}
          <dt>Running for</dt>
          <dd class="mono">{clock(now - msOf(scan.startedAt))}</dd>
        </dl>
        <div mix={actionsCss}>
          <a href={routes.securityScan.href()} mix={buttonStyle('primary')}>
            Open Security
          </a>
        </div>
      </>
    );
  };
}

/** A task completed today. */
function DonePane(handle: Handle<{ task: Task; taskKey: string; projectName: string | undefined; now: number; onOpen: () => void }>) {
  return () => {
    const { task, taskKey: key, projectName, now, onOpen } = handle.props;
    return (
      <>
        <div mix={paneHead}>
          <span mix={kindLine} style={{ color: t.success }}>
            {['Done', projectName].filter(Boolean).join(' · ')}
          </span>
          <h2>
            {key ? <span mix={keyCss}>{key}</span> : null}
            {visible(task.title)}
          </h2>
        </div>
        <dl mix={dlCss}>
          <dt>Completed</dt>
          <dd>{agoWords(msOf(task.completed), now)}</dd>
          {task.acceptedBy ? (
            <>
              <dt>Accepted by</dt>
              <dd>{visible(task.acceptedBy.replace(/^human:/, ''))}</dd>
            </>
          ) : null}
        </dl>
        <div mix={actionsCss}>
          <a href={routes.task.href({ id: task.id })} mix={[...buttonStyle('primary'), on('click', (e) => (e.preventDefault(), onOpen()))]}>
            Open the task
          </a>
        </div>
      </>
    );
  };
}

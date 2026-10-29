// The security scan screen (decisions P44, P61, P97 graphite): the header bar (the title, the target field the page gives,
// Scan again with R), three engine tiles, the line "N findings / Only files were read", the findings list (J/K or the arrows
// move), the dismissed findings under "N dismissed", the recent scans as chips, and the selected finding's pane beside them
// (finding-pane.tsx). The project's Security page and the workspace's Security scan both render ScanPanel over a scope
// (scan-scope.ts). The tools are Drydock's own (decision P95): a tile says whether its engine's tool is ready, nothing more. A
// finding names the rule, the file and the line; a secret's value is never shown. The core does the work (internal/scan); this
// is a projection of security.scan.get re-read on security.* events and, while a scan runs, read again for the counters: one
// read at a time, the next planned when the last has answered and later when the core is slow (lib/poll.ts).

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { Folder, ShieldCheck } from 'lucide';
import type { DismissReason, EngineID, EngineResult, ScanFinding, ScanReport, ScanSummary, SecurityTools } from '../../api/securityTypes.ts';
import type { DrydockEvent } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { typing } from '../../lib/keys.ts';
import { pollWhile } from '../../lib/poll.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { headerCss } from '../../ui/layout.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { listOf, WindowedList } from '../../ui/windowed-list.tsx';
import { Meter } from '../budgets/meter.tsx';
import { FindingPane, SeverityBadge } from './finding-pane.tsx';
import { summaryKey, type ScanScope } from './scan-scope.ts';
import {
  agoText,
  countsText,
  DISMISS_WORDS,
  emptyFindingsText,
  engineDeterminate,
  engineFraction,
  ENGINES,
  engineStateText,
  engineTone,
  engineWords,
  filesText,
  findingId,
  findingTitle,
  lineText,
  overallPercent,
  placeText,
  reportTarget,
  sortFindings,
  toolsUpdateText,
  SPLIT_QUERY,
  summaryTone,
  toolLabel,
} from './security.ts';

const HISTORY = 8;

/** Which tool of security.tools an engine uses. */
const ENGINE_TOOL: Record<EngineID, keyof Pick<SecurityTools, 'gitleaks' | 'osv' | 'opengrep'>> = { secrets: 'gitleaks', deps: 'osv', sast: 'opengrep' };

/** The recent scans a page lists, when they are not the scope's own (the workspace lists its folders' and its projects'). */
export interface ScanHistory {
  key: string;
  load(limit: number): Promise<ScanSummary[]>;
  when(e: DrydockEvent): boolean;
}

// Side by side (SPLIT_QUERY), each column scrolls on its own and the pane's actions stay in view; narrower, the pane goes
// under the list and the screen scrolls as one.
const screenCss = css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: '0', minWidth: '0', containerType: 'inline-size' });
const splitCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', display: 'flex', flexWrap: 'wrap', alignItems: 'stretch', [SPLIT_QUERY]: { overflow: 'hidden' } });
const scanCol = css({
  flex: '3 1 560px',
  minWidth: '0',
  display: 'flex',
  flexDirection: 'column',
  borderRight: `1px solid ${t.border}`,
  [SPLIT_QUERY]: { height: '100%', overflowY: 'auto' },
});
const barCss = css({ '& h1': { flex: 'none' } });
const titleIcon = css({ display: 'inline-flex', color: t.textMuted });
const kbdOn = css({ background: 'transparent', borderColor: 'currentColor', color: 'inherit', opacity: '0.7' });
const padCss = css({ padding: `0 ${t.s6}`, display: 'grid', gap: t.s2 });
const overallCss = css({ padding: `${t.s3} ${t.s6} 0` });

const tilesCss = css({ display: 'flex', flexWrap: 'wrap', gap: t.s3, padding: `${t.s4} ${t.s6}`, margin: '0', listStyle: 'none' });
const tileCss = css({
  flex: '1 1 180px',
  minWidth: '0',
  display: 'flex',
  flexDirection: 'column',
  gap: t.s2,
  padding: '14px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  containerType: 'inline-size',
  // the engine and its tool on the left, the state on the right (in a narrow tile the tool goes under them: narrowTool)
  '& .head': { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: `2px ${t.s2}`, minWidth: '0' },
  '& .name': { fontWeight: '600' },
  '& .tool': { color: t.textFaint, fontSize: t.textXs, minWidth: '0' },
  '& .state': { marginLeft: 'auto', fontSize: t.textXs, fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' },
  '& .count': { margin: '0', fontSize: t.text2xl, fontWeight: '600', lineHeight: '1.2', fontVariantNumeric: 'tabular-nums' },
  '& .count .words': { fontSize: t.textMd, fontWeight: '400', color: t.textMuted },
  '& .note': { margin: '0', color: t.textMuted, fontSize: t.textMd, overflowWrap: 'anywhere' },
});

const narrowTool = css({ '@container (max-width: 280px)': { order: '3', flexBasis: '100%' } });

const trackCss = css({ position: 'relative', height: '4px', borderRadius: t.rFull, background: t.bgActive, overflow: 'hidden', minWidth: '0' });
const fillCss = css({ position: 'absolute', inset: '0 auto 0 0', borderRadius: t.rFull, transition: `width ${t.med} ${t.ease}` });
// an engine that cannot count: moving stripes; with reduced motion the stripes stand still
const indeterminateCss = css({
  backgroundImage: `repeating-linear-gradient(90deg, ${t.accent} 0 12px, ${t.accentSoft} 12px 24px)`,
  animation: 'dd-scan-stripes 1.2s linear infinite',
  '@keyframes dd-scan-stripes': { from: { backgroundPosition: '0 0' }, to: { backgroundPosition: '24px 0' } },
  '@media (prefers-reduced-motion: reduce)': { animation: 'none' },
});

const lineCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: `${t.s1} 10px`,
  padding: `6px ${t.s6} ${t.s2}`,
  color: t.textFaint,
  fontSize: t.textXs,
  fontWeight: '500',
  '& .rule': { flex: '1 1 24px', height: '1px', background: t.border },
});

const listCss = css({ minWidth: '0' });
const rowCss = css({
  display: 'grid',
  gridTemplateColumns: '76px minmax(0, 1fr) 110px',
  alignItems: 'center',
  gap: t.s3,
  width: '100%',
  minHeight: '52px',
  padding: `10px ${t.s6}`,
  border: '0',
  borderRadius: '0',
  background: 'transparent',
  color: t.text,
  textAlign: 'left',
  '&:hover': { background: t.bgHover },
  '&[aria-current="true"]': { background: t.bgRaised, boxShadow: `inset 2px 0 0 ${t.accent}` },
  '& .what': { display: 'flex', flexDirection: 'column', gap: '2px', minWidth: '0' },
  '& .title': { fontWeight: '500', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .place': { fontFamily: t.mono, fontSize: t.textXs, color: t.textFaint, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .engine': { color: t.textMuted, fontSize: t.textXs },
});
const emptyCss = css({ margin: '0', padding: `${t.s2} ${t.s6}`, color: t.textMuted });

const dismissedCss = css({
  padding: `${t.s2} ${t.s6}`,
  display: 'grid',
  gap: t.s2,
  '& ul': { margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: '2px' },
  '& li': { display: 'flex', alignItems: 'center', gap: t.s3, minWidth: '0', color: t.textMuted },
  '& li .what': { display: 'flex', flexDirection: 'column', minWidth: '0', flex: '1' },
  '& li .title': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& li .place': { fontFamily: t.mono, fontSize: t.textXs, color: t.textFaint, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& li .why': { fontSize: t.textXs, color: t.textFaint, whiteSpace: 'nowrap' },
});

const historyCss = css({
  marginTop: 'auto',
  padding: `14px ${t.s6}`,
  borderTop: `1px solid ${t.border}`,
  display: 'flex',
  flexDirection: 'column',
  gap: t.s2,
  '& .label': { color: t.textFaint, fontSize: t.textXs, fontWeight: '500' },
  '& ul': { display: 'flex', flexWrap: 'wrap', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' },
  '& .none': { margin: '0', color: t.textMuted, fontSize: t.textXs },
});
const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: t.s2,
  height: '28px',
  maxWidth: '100%',
  padding: '0 10px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.text,
  fontSize: t.textMd,
  '&:hover': { background: t.bgHover },
  '&[aria-pressed="true"]': { background: t.bgRaised, borderColor: t.borderStrong },
  '& .name': { fontFamily: t.mono, fontSize: t.textXs, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: '0' },
  '& .count': { fontVariantNumeric: 'tabular-nums' },
  '& .when': { color: t.textFaint, whiteSpace: 'nowrap' },
});

const engineName = (id: EngineID): string => ENGINES.find((e) => e.id === id)?.name ?? id;

const targetCss = css({
  flex: '1 1 220px',
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  // the icon, 120 px of path and the Choose button: below that the header wraps it to a row of its own
  minWidth: '200px',
  height: '32px',
  padding: '0 10px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgInput,
  color: t.textFaint,
  '&:focus-within': { borderColor: t.borderStrong },
  '& > svg, & > .end': { flex: 'none' },
  '& > .end': { display: 'flex' },
  '& input, & .value': {
    flex: '1',
    // room to read and type a path: the note gives way first (at a 1300 px window the field was 23 px beside it)
    minWidth: '120px',
    height: '30px',
    padding: '0',
    border: '0',
    outline: '0',
    boxShadow: 'none',
    background: 'transparent',
    color: t.text,
    fontFamily: t.mono,
    fontSize: t.textXs,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  '& .value': { lineHeight: '30px' },
  '& .note': { flex: '0 1 auto', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', color: t.textFaint, fontSize: t.textXs, whiteSpace: 'nowrap' },
});
/** The header's target field: a folder icon, the path (an input or the repository's path, in mono) and a quiet note. */
export function TargetBox(handle: Handle<{ note?: string; end?: RemixNode; children?: RemixNode }>) {
  return () => (
    <div mix={targetCss} data-testid="scan-target">
      <Icon icon={Folder} size={14} />
      {handle.props.children}
      {handle.props.note ? (
        <span class="note" data-testid="scan-target-note">
          {handle.props.note}
        </span>
      ) : null}
      {handle.props.end ? <span class="end">{handle.props.end}</span> : null}
    </div>
  );
}

/** The header's middle: the page's target controls, taking the room between the title and Scan again. */
const targetSlot = css({ flex: '1 1 280px', display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, minWidth: '0' });


/** An engine's own bar while it runs: files while the secrets engine runs, moving stripes for an engine that cannot count. */
function EngineBar(handle: Handle<{ e: EngineResult }>) {
  return () => {
    const e = handle.props.e;
    const determinate = engineDeterminate(e);
    const pct = Math.round(engineFraction(e) * 100);
    return (
      <div
        mix={[trackCss, determinate ? undefined : indeterminateCss]}
        role="progressbar"
        aria-label={`${e.name} progress`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={determinate ? pct : undefined}
        aria-valuetext={determinate ? filesText(e) : 'in progress'}
        aria-busy="true"
        data-testid="engine-progress"
        data-determinate={determinate ? 'true' : 'false'}
      >
        {determinate ? <div mix={fillCss} style={{ width: `${pct}%`, background: tone.accent.fg }} /> : null}
      </div>
    );
  };
}

const stateTone = (e: EngineResult): Tone => (e.status === 'done' ? 'ok' : engineTone(e));

/** One engine's tile: the engine and its tool, its state and time, and what it found (or why it did not run); before any scan, whether its tool is ready. */
function EngineTile(handle: Handle<{ id: EngineID; e?: EngineResult; tools?: SecurityTools }>) {
  return () => {
    const { id, e, tools } = handle.props;
    const meta = ENGINES.find((x) => x.id === id)!;
    const tool = tools?.[ENGINE_TOOL[id]];
    const version = e?.version || tool?.version;
    const rules = id === 'sast' && tools?.builtinRuleCount ? ` · ${tools.builtinRuleCount.toLocaleString()} built-in rules` : '';
    const toolText = `${toolLabel(e?.tool || tool?.tool || meta.tool)}${rules}`;
    let state = '';
    let st: Tone = 'muted';
    let status: string;
    if (e) {
      state = engineStateText(e);
      st = stateTone(e);
      status = e.status;
    } else if (tool) {
      // the Code engine runs the built-in rules (decision P77), and a folder of the person's own when there is one
      const ok = tool.found && (id !== 'sast' || tools?.builtinRules === true || tools?.rulesOk === true);
      state = !tool.found ? 'not installed' : ok ? 'ready' : 'no rules';
      st = ok ? 'ok' : 'warn';
      status = ok ? 'ready' : 'missing';
    } else {
      status = 'unknown';
    }
    const counted = e && (e.status === 'done' || e.status === 'running');
    return (
      <li mix={tileCss} data-testid="scan-engine" data-engine={id} data-status={status}>
        <div class="head">
          <span class="name">{e?.name || meta.name}</span>
          <span class="tool" mix={narrowTool} title={version ? `${toolLabel(e?.tool || tool?.tool || meta.tool)} ${version}` : undefined}>
            {toolText}
          </span>
          {state ? (
            <span class="state" data-testid="engine-state" style={{ color: tone[st].fg }}>
              {state}
            </span>
          ) : null}
        </div>
        {counted ? (
          <p class="count" data-testid="engine-count">
            {e.findings.toLocaleString()} <span class="words">{engineWords(id, e.findings)}</span>
          </p>
        ) : null}
        {e?.reason && e.status !== 'done' ? (
          <p class="note" data-testid="engine-reason">
            {e.reason}
          </p>
        ) : null}
        {!e ? <p class="note">{meta.blurb}</p> : null}
        {e?.status === 'running' ? <EngineBar e={e} /> : null}
      </li>
    );
  };
}

/** One finding's row: severity, title and place, engine. */
function FindingRow(handle: Handle<{ f: ScanFinding; selected: boolean; onPick: () => void }>) {
  return () => {
    const { f, selected, onPick } = handle.props;
    const title = findingTitle(f);
    const place = placeText(f);
    return (
      <div role="listitem">
        <button
          type="button"
          mix={[rowCss, on('click', onPick)]}
          data-testid="finding"
          data-id={findingId(f)}
          data-engine={f.engine}
          data-severity={f.severity}
          data-rule={f.rule}
          data-fingerprint={f.fingerprint}
          aria-current={selected ? 'true' : undefined}
        >
          <SeverityBadge severity={f.severity} />
          <span class="what">
            <span class="title" title={title}>
              {title}
            </span>
            <span class="place" title={f.rule}>
              {place}
            </span>
          </span>
          <span class="engine">{engineName(f.engine)}</span>
        </button>
      </div>
    );
  };
}

/**
 * The scan screen over a scope (a project or a folder). `target` is the header's target field (the page's: a folder field, the
 * project's repository); `canScan` says whether Scan may be pressed (a project with a repository, a folder typed). A history
 * item of another folder or project is handed to `onPick` first, so the page can show it. `history` replaces the scope's own
 * history (the workspace lists every folder's and project's scans); `nameOf` names a scan on its chip.
 */
export function ScanPanel(
  handle: Handle<{
    scope: ScanScope;
    canScan: boolean;
    title: string;
    target: RemixNode;
    testId: string;
    nameOf: (h: ScanSummary) => string;
    history?: ScanHistory;
    notice?: RemixNode;
    onPick?: (h: ScanSummary) => void;
  }>,
) {
  const s = services(handle);
  const act = action(handle);
  const titleId = `page-title-${handle.id}`;
  const scope = () => handle.props.scope;
  const key = () => scope().key;
  const tools = query(handle, {
    load: () => scope().tools(),
    when: (e) => scope().toolsChanged(e),
    key,
  });
  const current = query(handle, {
    load: () => scope().get(),
    when: (e) => scope().mine(e),
    key,
  });
  const history = query(handle, {
    load: () => (handle.props.history ? handle.props.history.load(HISTORY) : scope().history(HISTORY)),
    when: (e) => (handle.props.history ? handle.props.history.when(e) : scope().mine(e)),
    key: () => handle.props.history?.key ?? key(),
  });

  // The counters of a running scan are not events: read them again while it runs (L37). One read at a time: the next is planned
  // when the last has answered, no sooner than 500 ms and no sooner than twice as long as that read took, and the poll ends with
  // the scan or with the panel (the signal).
  let scanRunning = false;
  let polling = false;
  const syncPoll = (running: boolean) => {
    scanRunning = running;
    if (!running || polling) return;
    polling = true;
    void pollWhile({ read: () => current.reload(), wanted: () => scanRunning, signal: handle.signal }).finally(() => {
      polling = false;
    });
  };

  // a report opened from the history (for the scope it was opened in); a running scan always takes over the display
  let opened: { report: ScanReport; key: string } | undefined;
  let shownId: string | undefined;
  /** The selected finding (findingId) and its place in the list, to select its neighbour when it leaves the list. */
  let selected = '';
  let selectedAt = 0;
  let showDismissed = false;
  /** What the page shows now, for the keyboard. */
  let active: ScanFinding[] = [];
  let canPress = false;

  const scan = async () => {
    const r = await act.run('scan', () => scope().start());
    if (r) {
      opened = undefined;
      current.reload();
      history.reload();
    }
  };
  const cancel = async () => {
    await act.run('cancel', () => scope().cancel());
    current.reload();
    history.reload();
  };
  const open = async (h: ScanSummary) => {
    handle.props.onPick?.(h);
    // a running scan is the scope's current one: the page shows it once the scope is that folder's
    if (h.status === 'running') {
      opened = undefined;
      void handle.update();
      return;
    }
    const r = await act.run('open', () => s.client.securityScanReport(h.id));
    if (r) {
      // the scope the page shows for it (a page that is handed the pick may not have re-rendered with it yet)
      opened = { report: r, key: handle.props.onPick ? summaryKey(h) : key() };
      void handle.update();
    }
  };
  /** A dismissal or a restore answered with the target's last report: show it, or read the opened older report again. */
  const adopt = async (r: ScanReport) => {
    if (opened && opened.report.id !== r.id) {
      const id = opened.report.id;
      const again = await act.run('open', () => s.client.securityScanReport(id));
      if (again && opened?.report.id === id) opened = { report: again, key: opened.key };
    } else if (opened) {
      opened = { report: r, key: opened.key };
    } else {
      current.set(r);
    }
    history.reload();
    void handle.update();
  };
  const dismiss = async (rep: ScanReport, f: ScanFinding, reason: DismissReason) => {
    if (!f.fingerprint) return;
    const r = await act.run('dismiss', () => s.client.securityFindingDismiss(reportTarget(rep), f.fingerprint!, reason));
    if (r) await adopt(r);
  };
  const restore = async (rep: ScanReport, f: ScanFinding) => {
    if (!f.fingerprint) return;
    const r = await act.run('restore', () => s.client.securityFindingRestore(reportTarget(rep), f.fingerprint!));
    if (r) await adopt(r);
  };

  // Update scanners: the newest OSV-Scanner and Opengrep, installed now (the core also checks twice a day); the tiles follow
  let updated = '';
  const updateTools = async () => {
    updated = '';
    const r = await act.run('update-tools', () => s.client.securityToolsUpdate());
    if (r) updated = toolsUpdateText(r);
    tools.reload();
    void handle.update();
  };

  const select = (id: string) => {
    selected = id;
    void handle.update();
  };
  /** Moves the selection by one; `focus` moves the keyboard with it (the arrows inside the list), else the row is scrolled to. */
  const move = (by: number, focus: boolean) => {
    if (active.length === 0) return;
    const at = Math.max(0, active.findIndex((f) => findingId(f) === selected));
    const next = active[Math.min(active.length - 1, Math.max(0, at + by))]!;
    selected = findingId(next);
    void handle.update();
    handle.queueTask(() => {
      const row = Array.from(document.querySelectorAll<HTMLElement>(`[data-testid="${handle.props.testId}"] [data-testid="finding"]`)).find((x) => x.dataset.id === selected);
      if (!row) return;
      if (focus) row.focus();
      else row.scrollIntoView?.({ block: 'nearest' });
    });
  };

  // R scans again; J and K move through the findings. Never while typing, in a dialog or menu, or inside a widget that owns its keys.
  window.addEventListener(
    'keydown',
    (e) => {
      if (typing(e)) return;
      const k = e.key.toLowerCase();
      if (k === 'r' && canPress) {
        e.preventDefault();
        void scan();
      } else if (k === 'j' || k === 'k') {
        e.preventDefault();
        move(k === 'j' ? 1 : -1, false);
      }
    },
    { signal: handle.signal },
  );
  const listKeys = (e: KeyboardEvent) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    e.preventDefault();
    move(e.key === 'ArrowDown' ? 1 : -1, true);
  };

  return () => {
    const { canScan, title, target, testId, notice, nameOf } = handle.props;
    const live = current.data ?? undefined;
    const running = live?.status === 'running';
    if (running || (opened && opened.key !== key())) opened = undefined;
    const report = running ? live : (opened?.report ?? live);
    if (report?.id !== shownId) {
      shownId = report?.id;
      showDismissed = false;
    }
    handle.queueTask(() => syncPoll(running));

    const engines: EngineResult[] = report?.engines ?? [];
    const engineOf = (id: EngineID) => engines.find((e) => e.id === id);
    const sorted = report ? sortFindings(report.findings) : [];
    active = sorted.filter((f) => !f.dismissed);
    const dismissed = sorted.filter((f) => f.dismissed);
    // the selection stays on its finding while it is listed, else moves to the one in its place
    let sel = active.findIndex((f) => findingId(f) === selected);
    if (sel < 0 && active.length > 0) {
      sel = Math.min(selectedAt, active.length - 1);
      selected = findingId(active[sel]!);
    }
    if (sel >= 0) selectedAt = sel;
    const finding = sel >= 0 ? active[sel] : undefined;
    const busy = act.busy !== '';
    canPress = canScan && !running && !busy;
    const pct = overallPercent(report);
    const list = history.data ?? [];
    const done = report && report.status !== 'running';
    const scanLabel = running || act.busy === 'scan' ? 'Scanning…' : report ? 'Scan again' : 'Scan';

    return (
      <section mix={screenCss} aria-labelledby={titleId} data-testid={testId}>
        <div mix={splitCss}>
          <section mix={scanCol} aria-label="Scan">
            <header mix={[headerCss, barCss]}>
              {/* the shield every page title has, as Page draws it (the UI audit of 2026-10-26: this header had none) */}
              <h1 id={titleId}>
                <span mix={titleIcon}>
                  <Icon icon={ShieldCheck} size={16} />
                </span>
                {title}
              </h1>
              <div mix={targetSlot}>{target}</div>
              <Button
                variant="ghost"
                data-testid="update-scanners"
                disabled={busy}
                title="Install the newest OSV-Scanner and Opengrep now (Gitleaks is built into Drydock and updates with the app)"
                mix={on('click', () => void updateTools())}
              >
                {act.busy === 'update-tools' ? 'Updating…' : 'Update scanners'}
              </Button>
              {running ? (
                <Button data-testid="cancel-button" disabled={busy} mix={on('click', () => void cancel())}>
                  Cancel
                </Button>
              ) : null}
              <Button variant="primary" data-testid="scan-button" disabled={!canPress} aria-keyshortcuts="R" mix={on('click', () => void scan())}>
                {scanLabel}
                <kbd mix={kbdOn}>R</kbd>
              </Button>
            </header>
            {notice || updated || act.error || current.error || history.error || report?.storeError ? (
              <div mix={padCss} style={{ paddingTop: t.s3 }}>
                {notice}
                {updated ? (
                  <Notice tone="ok" role="status" testId="scanners-updated">
                    {updated}
                  </Notice>
                ) : null}
                <ErrorNote error={act.error ?? current.error ?? history.error} what="Security scan" />
                {report?.storeError ? (
                  <Notice tone="warn" testId="scan-store-error">
                    {report.storeError}
                  </Notice>
                ) : null}
              </div>
            ) : null}
            {running ? (
              <div mix={overallCss}>
                <Meter value={pct / 100} label={`${pct}% of the scan`} tone="accent" testId="scan-overall" />
              </div>
            ) : null}

            <ul mix={tilesCss} aria-label="Engines">
              {ENGINES.map((m) => (
                <EngineTile key={m.id} id={m.id} e={engineOf(m.id)} tools={tools.data} />
              ))}
            </ul>

            <div mix={lineCss}>
              <span data-testid="scan-status" data-status={report?.status ?? 'idle'} role="status">
                {done && report.status === 'done' && report.counts.total > 0 ? <span data-testid="findings-count">{lineText(report)}</span> : lineText(report)}
              </span>
              <span class="rule" aria-hidden="true" />
              <span data-testid="scan-reads-only">{report ? 'Only files were read. Nothing in the folder ran.' : 'Only files are read. Nothing in the folder runs.'}</span>
            </div>

            {done && active.length === 0 ? (
              <p mix={emptyCss} data-testid="findings-empty" data-checked={String(emptyFindingsText(report) === 'Nothing found.')}>
                {dismissed.length > 0 && emptyFindingsText(report) === 'Nothing found.' ? 'Nothing left to look at.' : emptyFindingsText(report)}
              </p>
            ) : null}
            {active.length > 0 ? (
              <div mix={[listCss, on('keydown', (e) => listKeys(e as KeyboardEvent))]}>
                <WindowedList
                  {...listOf(
                    active,
                    (f) => findingId(f),
                    (f) => <FindingRow f={f} selected={findingId(f) === selected} onPick={() => select(findingId(f))} />,
                  )}
                  label="Findings"
                  role="list"
                  testId="findings"
                  estimate={52}
                  maxHeight={520}
                  mix={[listCss]}
                />
              </div>
            ) : null}
            {report?.truncated ? (
              <p mix={emptyCss} data-testid="findings-truncated">
                Showing the first {report.findings.length.toLocaleString()}; the rest were counted but not kept.
              </p>
            ) : null}
            {report && dismissed.length > 0 ? (
              <div mix={dismissedCss} data-testid="findings-dismissed">
                <Button
                  variant="ghost"
                  size="sm"
                  data-testid="dismissed-toggle"
                  aria-expanded={showDismissed ? 'true' : 'false'}
                  style={{ justifySelf: 'start' }}
                  mix={on('click', () => {
                    showDismissed = !showDismissed;
                    void handle.update();
                  })}
                >
                  {dismissed.length.toLocaleString()} dismissed
                </Button>
                {showDismissed ? (
                  <ul aria-label="Dismissed findings">
                    {dismissed.map((f) => (
                      <li key={findingId(f)} data-testid="dismissed-finding" data-fingerprint={f.fingerprint}>
                        <span class="what">
                          <span class="title">{findingTitle(f)}</span>
                          <span class="place">{placeText(f)}</span>
                        </span>
                        {f.dismissReason ? <span class="why">{DISMISS_WORDS[f.dismissReason]}</span> : null}
                        <Button size="sm" data-testid="finding-restore" disabled={busy} aria-label={`Restore ${findingTitle(f)}`} mix={on('click', () => void restore(report, f))}>
                          Restore
                        </Button>
                      </li>
                    ))}
                  </ul>
                ) : null}
              </div>
            ) : null}

            <section mix={historyCss} aria-label="Recent scans" data-testid="scan-history">
              <span class="label">Recent scans</span>
              {history.data && list.length === 0 ? <p class="none">No scans yet.</p> : null}
              {list.length > 0 ? (
                <ul>
                  {list.map((h) => {
                    const name = nameOf(h);
                    const n = h.counts.total;
                    return (
                      <li key={h.id}>
                        <button
                          type="button"
                          data-testid="scan-history-item"
                          data-id={h.id}
                          data-folder={h.folder}
                          data-project={h.projectId || undefined}
                          aria-pressed={report?.id === h.id ? 'true' : 'false'}
                          title={`${h.folder ?? name}: ${h.status === 'running' ? 'in progress' : countsText(h.counts)}, ${formatDateTime(h.startedAt)}`}
                          disabled={busy}
                          mix={[chipCss, on('click', () => void open(h))]}
                        >
                          <span class="name">{name}</span>
                          <span class="count" style={{ color: tone[summaryTone(h)].fg }}>
                            {h.status === 'running' ? 'scanning' : h.status === 'done' ? n.toLocaleString() : h.status}
                          </span>
                          <span class="when">{agoText(h.startedAt)}</span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              ) : null}
            </section>
          </section>

          <FindingPane
            report={report}
            finding={finding}
            busy={busy}
            onDismiss={(f, reason) => (report ? dismiss(report, f, reason) : Promise.resolve())}
          />
        </div>
      </section>
    );
  };
}

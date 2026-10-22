// The security scan's shared parts (decisions P44, P61): the Scan button with its status and progress, one row per engine,
// the findings with their filters, and the history. The tools are Drydock's own (decision P95): installed and kept current by
// the core, so the page shows no tools section, only each engine's readiness. The project's Security
// page and the workspace Security page both render ScanPanel over a scope (scan-scope.ts). A finding names the rule, the file
// and the line; a secret's value is never shown. The core does the work (internal/scan); this is a projection of
// security.scan.get re-read on security.* events and, while a scan runs, read again for the counters: one read at a time, the next
// planned when the last has answered and later when the core is slow (lib/poll.ts).

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { KeyRound, Package, ScanSearch, type IconNode } from 'lucide';
import type { EngineID, EngineResult, ScanFinding, ScanReport, ScanSummary, SecurityTools, Severity } from '../../api/securityTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import { pollWhile } from '../../lib/poll.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Section, card, listCard } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { listOf, WindowedList } from '../../ui/windowed-list.tsx';
import { Meter } from '../budgets/meter.tsx';
import type { ScanScope } from './scan-scope.ts';
import {
  advisoryUrl,
  byName,
  countsText,
  durationText,
  elapsed,
  emptyFindingsText,
  engineCounts,
  engineDeterminate,
  engineFraction,
  ENGINES,
  engineTone,
  filesText,
  filterFindings,
  findingsText,
  overallPercent,
  SEVERITIES,
  severityTone,
  sortFindings,
  statusText,
  whereText,
} from './security.ts';

const HISTORY = 10;

const ENGINE_ICON: Record<EngineID, IconNode> = { secrets: KeyRound, deps: Package, sast: ScanSearch };
/** Which tool of security.tools an engine uses. */
const ENGINE_TOOL: Record<EngineID, keyof Pick<SecurityTools, 'gitleaks' | 'osv' | 'opengrep'>> = { secrets: 'gitleaks', deps: 'osv', sast: 'opengrep' };

/** The page's opening paragraph. */
const dirCss = css({ margin: '0', color: t.textMuted, fontSize: t.textSm, overflowWrap: 'anywhere' });
const actionRow = css({ display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' });
const statusCss = css({ color: t.textMuted, fontSize: t.textMd, minWidth: '0', overflowWrap: 'anywhere' });
const overallCss = css({ display: 'grid', gap: '6px', '& > p': { margin: '0', fontSize: t.textSm, color: t.textMuted, fontVariantNumeric: 'tabular-nums' } });

const engineRow = css({
  display: 'grid',
  gridTemplateColumns: '20px minmax(0, 1fr) auto',
  gap: `${t.s2} ${t.s3}`,
  alignItems: 'start',
  '& .ico': { display: 'inline-flex', color: t.textMuted, marginTop: '2px' },
  '& .head': { display: 'flex', alignItems: 'baseline', gap: t.s2, flexWrap: 'wrap', minWidth: '0' },
  '& .head strong': { fontWeight: '600' },
  '& .blurb': { margin: '2px 0 0', color: t.textMuted, fontSize: t.textSm },
  '& .bar': { display: 'grid', gap: '4px', marginTop: t.s2 },
  '& .bar p': { margin: '0', fontSize: t.textSm, color: t.textMuted, fontVariantNumeric: 'tabular-nums' },
  '& .right': { display: 'grid', gap: '4px', justifyItems: 'end', textAlign: 'right', fontSize: t.textSm, color: t.textMuted, whiteSpace: 'nowrap' },
  '& .reason': { margin: '0', whiteSpace: 'normal', maxWidth: '36ch', overflowWrap: 'anywhere' },
});

const trackCss = css({ position: 'relative', height: '6px', borderRadius: t.rFull, background: t.bgActive, overflow: 'hidden', minWidth: '0' });
const fillCss = css({ position: 'absolute', inset: '0 auto 0 0', borderRadius: t.rFull, transition: `width ${t.med} ${t.ease}` });
// an engine that cannot count: moving stripes; with reduced motion the stripes stand still
const indeterminateCss = css({
  backgroundImage: `repeating-linear-gradient(90deg, ${t.accent} 0 12px, ${t.accentSoft} 12px 24px)`,
  animation: 'dd-scan-stripes 1.2s linear infinite',
  '@keyframes dd-scan-stripes': { from: { backgroundPosition: '0 0' }, to: { backgroundPosition: '24px 0' } },
  '@media (prefers-reduced-motion: reduce)': { animation: 'none' },
});

const chipRow = css({ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' });
const chipCss = css({
  height: '24px',
  padding: '0 10px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  background: t.bgSurface,
  color: t.textMuted,
  fontSize: t.textSm,
  fontWeight: '500',
  whiteSpace: 'nowrap',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-pressed="true"]': { background: t.bgActive, color: t.text, borderColor: t.borderStrong },
});
const chipSep = css({ width: '1px', height: '16px', background: t.border, margin: '0 4px' });
const countCss = css({ marginLeft: 'auto', color: t.textMuted, fontSize: t.textSm, fontVariantNumeric: 'tabular-nums' });

// The findings are a table of rows drawn with a grid, so a long list can be windowed (ui/windowed-list.tsx, L37): the header row and
// each finding share these columns. They carry the roles of a table; the first row is the header.
const FINDING_COLUMNS = 'minmax(78px, 92px) minmax(84px, 112px) minmax(140px, 1.2fr) minmax(150px, 1.5fr) minmax(190px, 2fr)';
const findingsTable = css({
  minWidth: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  fontSize: t.textSm,
  '& [role="row"]': { display: 'grid', gridTemplateColumns: FINDING_COLUMNS, minWidth: '720px' },
  '& [role="columnheader"]': { padding: '8px 12px', borderBottom: `1px solid ${t.border}`, color: t.textMuted, fontWeight: '500', textAlign: 'left', whiteSpace: 'nowrap' },
  '& [role="cell"]': { padding: '8px 12px', borderBottom: `1px solid ${t.border}`, minWidth: '0', overflowWrap: 'anywhere' },
  '& [role="cell"].what': { minWidth: '18ch' },
  '& [data-testid="finding"]:hover [role="cell"]': { background: t.bgHover },
  '& .ids': { display: 'inline-flex', gap: '4px', flexWrap: 'wrap', marginLeft: '6px', verticalAlign: 'middle' },
});
const findingsHead = css({ position: 'sticky', top: '0', zIndex: '1', background: t.bgSurface });
const idBadge = css({
  display: 'inline-flex',
  alignItems: 'center',
  height: '18px',
  padding: '0 6px',
  borderRadius: t.rSm,
  border: `1px solid ${t.border}`,
  background: t.bgApp,
  color: t.accentText,
  fontFamily: t.mono,
  fontSize: t.textXs,
  textDecoration: 'none',
  '&:hover': { textDecoration: 'underline' },
});

const historyBtn = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flexWrap: 'wrap',
  width: '100%',
  padding: '0',
  border: '0',
  background: 'transparent',
  color: t.text,
  fontSize: t.textMd,
  textAlign: 'left',
  '& .when': { fontVariantNumeric: 'tabular-nums' },
  '& .by, & .counts': { color: t.textMuted, fontSize: t.textSm },
  '& .folder': { fontFamily: t.mono, fontSize: t.textSm, color: t.textMuted, overflowWrap: 'anywhere', minWidth: '0' },
  '&:hover': { color: t.text },
  '&[aria-pressed="true"] .when': { fontWeight: '600' },
});

const engineLabel = (id: EngineID): string => ENGINES.find((e) => e.id === id)?.name ?? id;

/** Names one place in the list of findings: what the finding is, and where it is in the list shown. */
const findingKey = (f: ScanFinding, i: number): string => `${f.engine}:${f.rule}:${f.file ?? ''}:${f.line ?? 0}:${f.package ?? ''}:${i}`;

/** An engine's own bar: files while the secrets engine runs, moving stripes for an engine that cannot count, full when it finished. */
function EngineBar(handle: Handle<{ e: EngineResult }>) {
  return () => {
    const e = handle.props.e;
    const running = e.status === 'running';
    const determinate = !running || engineDeterminate(e);
    const pct = Math.round(engineFraction(e) * 100);
    return (
      <div
        mix={[trackCss, determinate ? undefined : indeterminateCss]}
        role="progressbar"
        aria-label={`${e.name} progress`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={determinate ? pct : undefined}
        aria-valuetext={!determinate ? 'in progress' : engineDeterminate(e) ? filesText(e) : `${pct}%`}
        aria-busy={running ? 'true' : undefined}
        data-testid="engine-progress"
        data-determinate={determinate ? 'true' : 'false'}
      >
        {determinate ? <div mix={fillCss} style={{ width: `${pct}%`, background: tone[engineTone(e)].fg }} /> : null}
      </div>
    );
  };
}

/** The words of an engine's state: the badge and, under it, the count, the time and the reason. */
function engineState(e: EngineResult): { badge: string; lines: RemixNode[] } {
  const lines: RemixNode[] = [];
  const took = elapsed(e);
  switch (e.status) {
    case 'done':
      lines.push(<span key="n">{findingsText(e.findings)}</span>);
      if (took !== undefined) lines.push(<span key="t">in {durationText(took)}</span>);
      return { badge: 'done', lines };
    case 'running':
      return { badge: 'running', lines };
    case 'pending':
      return { badge: 'waiting', lines };
    case 'skipped':
    case 'errored':
    case 'cancelled':
      if (e.reason) {
        lines.push(
          <p key="r" class="reason" data-testid="engine-reason">
            {e.reason}
          </p>,
        );
      }
      if (took !== undefined && e.status !== 'skipped') lines.push(<span key="t">after {durationText(took)}</span>);
      return { badge: e.status, lines };
    default:
      return { badge: e.status, lines };
  }
}

/** One engine: its result when a scan is shown, else the tool's readiness. */
function EngineRow(handle: Handle<{ id: EngineID; e?: EngineResult; tools?: SecurityTools }>) {
  return () => {
    const { id, e, tools } = handle.props;
    const meta = ENGINES.find((x) => x.id === id)!;
    const tool = tools?.[ENGINE_TOOL[id]];
    const name = e?.name || meta.name;
    const toolName = e?.tool || tool?.tool || meta.tool;
    const version = e?.version || tool?.version;
    let badge: RemixNode;
    let right: RemixNode[] = [];
    let status: string;
    if (e) {
      const st = engineState(e);
      badge = <Badge tone={engineTone(e)}>{st.badge}</Badge>;
      right = st.lines;
      status = e.status;
    } else if (tool) {
      // the Code engine runs the built-in rules (decision P77), and a folder of the person's own when there is one
      const ok = tool.found && (id !== 'sast' || tools?.builtinRules === true || tools?.rulesOk === true);
      badge = <Badge tone={ok ? 'ok' : 'warn'}>{!tool.found ? 'not installed' : ok ? 'ready' : 'no rules'}</Badge>;
      status = ok ? 'ready' : 'missing';
    } else {
      badge = <Badge tone="muted">unknown</Badge>;
      status = 'unknown';
    }
    return (
      <li mix={engineRow} data-testid="scan-engine" data-engine={id} data-status={status}>
        <span class="ico">
          <Icon icon={ENGINE_ICON[id]} size={16} />
        </span>
        <div>
          <div class="head">
            <strong>{name}</strong>
            <span mix={mono}>
              {toolName}
              {version ? ` ${version}` : ''}
            </span>
          </div>
          <p class="blurb">{meta.blurb}</p>
          {e ? (
            <div class="bar">
              <EngineBar e={e} />
              {e.status === 'running' && engineDeterminate(e) ? <p>{filesText(e)}</p> : null}
            </div>
          ) : null}
        </div>
        <div class="right">
          {badge}
          {right}
        </div>
      </li>
    );
  };
}

function Chip(handle: Handle<{ testId: string; severity?: string; engine?: string; pressed: boolean; onPick: () => void; children?: RemixNode }>) {
  return () => {
    const { testId, severity, engine, pressed, onPick, children } = handle.props;
    return (
      <button type="button" data-testid={testId} data-severity={severity} data-engine={engine} aria-pressed={pressed ? 'true' : 'false'} mix={[chipCss, on('click', onPick)]}>
        {children}
      </button>
    );
  };
}

function Where(handle: Handle<{ f: ScanFinding }>) {
  return () => {
    const f = handle.props.f;
    const ids = f.ids ?? [];
    return (
      <>
        <span mix={mono}>{whereText(f)}</span>
        {f.engine === 'deps' && ids.length > 0 ? (
          <span class="ids">
            {ids.map((id) => (
              <a key={id} href={advisoryUrl(id)} target="_blank" rel="noreferrer" data-rmx-document mix={idBadge} title={`${id} on osv.dev`}>
                {id}
              </a>
            ))}
          </span>
        ) : null}
      </>
    );
  };
}

/**
 * The scan itself, shared by the project's Security page and the workspace Security page (decision P61): the Scan button and
 * its status, the overall and per-engine progress, the findings and the history. It reads and acts only through `scope` (a project or a folder); `canScan` says whether Scan may be pressed (a
 * project with a repository, a folder typed). A history item of another folder is handed to `onPick` first, so the page can
 * show that folder.
 */
export function ScanPanel(
  handle: Handle<{ scope: ScanScope; canScan: boolean; historyTitle?: string; onPick?: (h: ScanSummary) => void }>,
) {
  const s = services(handle);
  const act = action(handle);
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
    load: () => scope().history(HISTORY),
    when: (e) => scope().mine(e),
    key,
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
  let sev: Severity | 'all' = 'all';
  let eng: EngineID | 'all' = 'all';
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
      opened = { report: r, key: key() };
      void handle.update();
    }
  };
  const pick = (f: () => void) => {
    f();
    void handle.update();
  };

  return () => {
    const { canScan, historyTitle } = handle.props;
    const folderScope = scope().kind === 'folder';
    const live = current.data ?? undefined;
    const running = live?.status === 'running';
    if (running || (opened && opened.key !== key())) opened = undefined;
    const report = running ? live : (opened?.report ?? live);
    if (report?.id !== shownId) {
      shownId = report?.id;
      sev = 'all';
      eng = 'all';
    }
    handle.queueTask(() => syncPoll(running));

    const engines: EngineResult[] = report?.engines ?? [];
    const engineOf = (id: EngineID) => engines.find((e) => e.id === id);
    const sorted = report ? sortFindings(report.findings) : [];
    const perEngine = engineCounts(sorted);
    const shown = filterFindings(sorted, sev, eng);
    const pct = overallPercent(report);
    const busy = act.busy !== '';
    const tl = tools.data;
    const list = history.data ?? [];

    return (
      <>
        <ErrorNote error={current.error ?? history.error} what="Security scan" />
        <div mix={card} style={{ display: 'grid', gap: t.s3 }}>
          <div mix={actionRow}>
            <Button variant="primary" data-testid="scan-button" disabled={!canScan || running || busy} mix={on('click', () => void scan())}>
              {running || act.busy === 'scan' ? 'Scanning…' : 'Scan'}
            </Button>
            {running ? (
              <Button data-testid="cancel-button" disabled={busy} mix={on('click', () => void cancel())}>
                Cancel
              </Button>
            ) : null}
            <span mix={statusCss} data-testid="scan-status" data-status={report?.status ?? 'idle'} role="status">
              {statusText(report)}
            </span>
          </div>
          {folderScope && report?.dir ? (
            <p mix={dirCss} data-testid="scan-dir">
              Folder <span mix={mono}>{report.dir}</span>
            </p>
          ) : null}
          <ErrorNote error={act.error} />
          {report?.storeError ? (
            <Notice tone="warn" testId="scan-store-error">
              {report.storeError}
            </Notice>
          ) : null}
          {report ? (
            <div mix={overallCss}>
              <Meter value={pct / 100} label={`${pct}% of the scan`} tone={running ? 'accent' : report.status === 'done' ? 'ok' : 'muted'} testId="scan-overall" />
              <p aria-hidden="true">{pct}%</p>
            </div>
          ) : null}
        </div>

        <Section title="Engines" description={report ? undefined : 'What each engine looks for, and whether its tool is on this computer.'}>
          {!report && !tl && tools.loading ? <Loading /> : null}
          <ul mix={listCard} aria-label="Engines">
            {ENGINES.map((m) => (
              <EngineRow key={m.id} id={m.id} e={engineOf(m.id)} tools={report ? undefined : tl} />
            ))}
          </ul>
        </Section>

        {report && report.status !== 'running' && sorted.length === 0 ? (
          <Section title="Findings">
            <p mix={[card, muted]} data-testid="findings-empty" data-checked={String(emptyFindingsText(report) === 'Nothing found.')}>
              {emptyFindingsText(report)}
            </p>
          </Section>
        ) : null}
        {report && sorted.length > 0 ? (
          <Section title="Findings">
            <div mix={chipRow} role="group" aria-label="Filter findings">
              <Chip testId="sev-filter" severity="all" pressed={sev === 'all'} onPick={() => pick(() => (sev = 'all'))}>
                All {sorted.length.toLocaleString()}
              </Chip>
              {SEVERITIES.filter((x) => report.counts[x] > 0 || sorted.some((f) => f.severity === x)).map((x) => (
                <Chip key={x} testId="sev-filter" severity={x} pressed={sev === x} onPick={() => pick(() => (sev = x))}>
                  {x} {sorted.filter((f) => f.severity === x).length.toLocaleString()}
                </Chip>
              ))}
              <span mix={chipSep} aria-hidden="true" />
              <Chip testId="engine-filter" engine="all" pressed={eng === 'all'} onPick={() => pick(() => (eng = 'all'))}>
                All engines
              </Chip>
              {ENGINES.filter((m) => perEngine[m.id] > 0).map((m) => (
                <Chip key={m.id} testId="engine-filter" engine={m.id} pressed={eng === m.id} onPick={() => pick(() => (eng = m.id))}>
                  {m.name} {perEngine[m.id].toLocaleString()}
                </Chip>
              ))}
              <span mix={countCss} data-testid="findings-count">
                {shown.length === sorted.length ? findingsText(shown.length) : `${shown.length.toLocaleString()} of ${findingsText(sorted.length)}`}
              </span>
            </div>
            <WindowedList
              {...listOf(shown, findingKey, (f, i) => (
                <div role="row" aria-rowindex={i + 2} data-testid="finding" data-engine={f.engine} data-severity={f.severity}>
                  <div role="cell">
                    <Badge tone={severityTone(f.severity)}>{f.severity}</Badge>
                  </div>
                  <div role="cell">{engineLabel(f.engine)}</div>
                  <div role="cell">
                    <code>{f.rule}</code>
                  </div>
                  <div role="cell" class="where">
                    <Where f={f} />
                  </div>
                  <div role="cell" class="what">
                    {f.title}
                    {f.engine === 'secrets' ? ' The value is not shown.' : ''}
                  </div>
                </div>
              ))}
              label="Findings"
              role="table"
              testId="findings"
              ariaRowCount={shown.length + 1}
              estimate={44}
              maxHeight={560}
              mix={[findingsTable]}
              header={
                <div role="row" aria-rowindex={1} mix={findingsHead}>
                  <div role="columnheader">Severity</div>
                  <div role="columnheader">Engine</div>
                  <div role="columnheader">Rule</div>
                  <div role="columnheader">Where</div>
                  <div role="columnheader">What</div>
                </div>
              }
            />
            {report.truncated ? (
              <p mix={muted} data-testid="findings-truncated">
                Showing the first {report.findings.length.toLocaleString()}; the rest were counted but not kept.
              </p>
            ) : null}
          </Section>
        ) : null}

        <Section title={historyTitle ?? 'History'} testId="scan-history">
          {history.data && list.length === 0 ? <p mix={[card, muted]}>No scans yet.</p> : null}
          {list.length > 0 ? (
            <ul mix={listCard} aria-label="Past scans">
              {list.map((h) => (
                <li key={h.id}>
                  <button
                    type="button"
                    data-testid="scan-history-item"
                    data-id={h.id}
                    data-folder={h.folder}
                    aria-pressed={report?.id === h.id ? 'true' : 'false'}
                    disabled={busy}
                    mix={[historyBtn, on('click', () => void open(h))]}
                  >
                    <span class="when">{formatDateTime(h.startedAt)}</span>
                    {h.folder ? <span class="folder">{h.folder}</span> : null}
                    <span class="by">by {byName(h.by)}</span>
                    <Badge tone={h.status === 'done' ? (h.counts.total > 0 ? 'warn' : 'ok') : h.status === 'running' ? 'accent' : 'muted'}>{h.status}</Badge>
                    <span class="counts">{h.status === 'running' ? 'in progress' : countsText(h.counts)}</span>
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </Section>
      </>
    );
  };
}

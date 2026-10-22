// The selected finding's pane on the Security scan screen (decision P97): its severity and engine, its title, what it means in
// plain words, the code around it with every secret drawn as a "hidden" chip (the core masks them; the value never reaches the
// page), what to do, and three actions: make a task from it for an agent of the scan's project, open its whole file read-only
// (security.file.read: Drydock opens no editor and runs nothing), and dismiss it ("Not a real key", a false positive, an
// accepted risk). The lines are read from the scanned folder when the finding is shown (security.finding.context).

import { css, on, type Handle } from 'remix/component';
import type { DismissReason, ScanContextLine, ScanFinding, ScanFindingContext, ScanReport, Severity } from '../../api/securityTypes.ts';
import { routes } from '../../routes.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Menu, type MenuItem } from '../../ui/menu.tsx';
import { Select } from '../../ui/select.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { ENGINES, findingTitle, lineParts, placeText, refLabel, refUrl, severityLabel, severityTone, SPLIT_QUERY, taskAgents } from './security.ts';

const sevCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  height: '20px',
  padding: '0 8px',
  borderRadius: t.rSm,
  fontSize: t.textXs,
  fontWeight: '600',
  whiteSpace: 'nowrap',
  justifySelf: 'start',
});

/** A severity's badge: Critical solid red, High red, Medium amber, Low blue, Info grey (the word is always there). */
export function SeverityBadge(handle: Handle<{ severity: Severity | string }>) {
  return () => {
    const sv = handle.props.severity;
    const c = sv === 'critical' ? { fg: t.onAccent, bg: t.dangerSolid } : tone[severityTone(sv)];
    return (
      <span mix={sevCss} data-testid="severity" data-severity={sv} style={{ color: c.fg, background: c.bg }}>
        {severityLabel(sv)}
      </span>
    );
  };
}

const paneCss = css({
  flex: '2 1 380px',
  minWidth: '0',
  background: t.bgSidebar,
  // the pane's content sits 8 px in from its edges, its separators too: a column of text never touches the list's
  '& > .inner': { display: 'flex', flexDirection: 'column', minHeight: '100%', margin: `0 ${t.s2}` },
  // beside the list it scrolls on its own (scan-panel.tsx, SPLIT_QUERY)
  [SPLIT_QUERY]: { height: '100%', overflowY: 'auto' },
  '& .head': { padding: `18px ${t.s4}`, borderBottom: `1px solid ${t.border}`, display: 'flex', flexDirection: 'column', gap: '10px' },
  '& .sev': { fontSize: t.textXs, fontWeight: '500' },
  '& h2': { margin: '0', fontSize: t.textXl, fontWeight: '600', lineHeight: '1.35', overflowWrap: 'anywhere' },
  '& .meaning': { margin: '0', color: t.textMuted, lineHeight: '1.55' },
  '& .body': { display: 'grid', gap: t.s4, padding: t.s4 },
  '& .note': { margin: '0', color: t.textMuted, fontSize: t.textMd },
  '& .label': { color: t.textFaint, fontSize: t.textXs, fontWeight: '500' },
  '& ol': { margin: '0', paddingLeft: '18px', color: t.text, lineHeight: '1.6', display: 'grid', gap: t.s1 },
  '& .refs': { display: 'flex', flexWrap: 'wrap', gap: `${t.s1} ${t.s3}`, fontSize: t.textXs, alignItems: 'center' },
  '& .refs a': { display: 'inline-flex', alignItems: 'center', minHeight: '28px', fontFamily: t.mono },
  '& .actions': { marginTop: 'auto', padding: t.s4, display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '10px', borderTop: `1px solid ${t.border}` },
  '& .actions .end': { marginLeft: 'auto' },
  '& .empty': { margin: '0', padding: `18px ${t.s4}`, color: t.textMuted },
});

const codeCss = css({
  margin: '0',
  padding: `${t.s3} 0`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  color: t.text,
  fontFamily: t.mono,
  fontSize: t.textXs,
  lineHeight: '1.7',
  overflowX: 'auto',
  whiteSpace: 'pre',
  '& .l': { display: 'block', padding: `0 ${t.s3}`, minWidth: 'max-content' },
  '& .l[data-hit="true"]': { background: t.accentSoft, boxShadow: `inset 2px 0 0 ${t.accent}` },
  '& .n': { display: 'inline-block', minWidth: '3ch', marginRight: t.s3, color: t.textFaint, textAlign: 'right', userSelect: 'none' },
  '& .mask': { padding: '0 4px', borderRadius: '3px', background: t.dangerSoft, color: t.danger },
});

/** The lines of a file as the core read them: numbers, the finding's own lines marked, every mask a "hidden" chip. */
export function CodeLines(handle: Handle<{ lines: readonly ScanContextLine[]; label: string; testId: string }>) {
  return () => {
    const { lines, label, testId } = handle.props;
    return (
      <pre mix={codeCss} aria-label={label} data-testid={testId} tabIndex={0}>
        {lines.map((l) => (
          <span key={l.n} class="l" data-n={l.n} data-hit={l.hit ? 'true' : undefined} data-masked={l.masked ? 'true' : undefined}>
            <span class="n" aria-hidden="true">
              {l.n}
            </span>
            {lineParts(l.text).map((p, i) =>
              'mask' in p ? (
                <span key={i} class="mask" data-testid="masked-secret" title="A secret: Drydock never shows its value">
                  <span aria-hidden="true">••••••••</span> hidden
                </span>
              ) : (
                <span key={i}>{p.text}</span>
              ),
            )}
            {l.cut ? <span class="n"> …</span> : null}
          </span>
        ))}
      </pre>
    );
  };
}

const DISMISS_SECRET: MenuItem[] = [
  { name: 'not_real', label: 'Not a real key' },
  { name: 'false_positive', label: 'The rule is wrong here' },
  { name: 'accepted_risk', label: 'A risk I accept' },
];
const DISMISS_OTHER: MenuItem[] = [
  { name: 'false_positive', label: 'The rule is wrong here' },
  { name: 'accepted_risk', label: 'A risk I accept' },
  { name: 'not_real', label: 'Not a real problem' },
];

/**
 * The pane beside the findings. `report` and `finding` are what the list shows and selects; `onDismiss` dismisses the finding
 * (the list owns the report, which changes). A folder scan belongs to no project and cannot make a task: the button says why.
 */
export function FindingPane(
  handle: Handle<{ report: ScanReport | undefined; finding: ScanFinding | undefined; busy?: boolean; onDismiss: (f: ScanFinding, reason: DismissReason) => Promise<void> }>,
) {
  const s = services(handle);
  const taskAct = action(handle);
  const fileAct = action(handle);
  const scanId = () => handle.props.report?.id ?? '';
  const fp = () => handle.props.finding?.fingerprint ?? '';
  const pid = () => handle.props.report?.projectId ?? '';
  const cx = query(handle, {
    load: () => s.client.securityFindingContext(scanId(), fp()),
    key: () => [scanId(), fp()],
    enabled: () => scanId() !== '' && fp() !== '',
  });
  const agents = query(handle, {
    load: () => s.client.agentList(pid()),
    when: (e) => e.type.startsWith('agent.') && e.projectId === pid(),
    key: () => pid(),
    enabled: () => pid() !== '',
  });
  let agentId = '';
  /** The task made from a finding (by scan and fingerprint), shown while that finding is. */
  let made: { at: string; id: string; title: string } | undefined;
  let fileOpen = false;
  let file: ScanFindingContext | undefined;
  /** The finding the last task attempt was for: its error is shown with that finding only. */
  let taskAt = '';

  const makeTask = async (assignee: string) => {
    const at = `${scanId()}:${fp()}`;
    taskAt = at;
    const r = await taskAct.run('task', () => s.client.securityFindingTask(scanId(), fp(), assignee));
    if (r) made = { at, id: r.task.id, title: r.task.title };
    void handle.update();
  };
  const openFile = async () => {
    fileOpen = true;
    file = undefined;
    const at = `${scanId()}:${fp()}`;
    const r = await fileAct.run('file', () => s.client.securityFileRead(scanId(), fp()));
    if (r && at === `${scanId()}:${fp()}`) file = r;
    void handle.update();
  };
  const closeFile = () => {
    fileOpen = false;
    file = undefined;
    fileAct.clear();
  };

  return () => {
    const { report, finding: f, busy } = handle.props;
    if (!report || !f) {
      return (
        <aside mix={paneCss} aria-label="Finding" data-testid="finding-pane">
          <div class="inner">
            <p class="empty">{report && report.status !== 'running' ? 'Nothing to look at: the scan found nothing to show.' : report ? 'The findings appear here as the engines finish.' : 'Scan to see what it finds. A finding opens here with the code around it.'}</p>
          </div>
        </aside>
      );
    }
    const at = `${report.id}:${f.fingerprint ?? ''}`;
    const engine = ENGINES.find((e) => e.id === f.engine)?.name ?? f.engine;
    const c = cx.data && cx.data.scanId === report.id && cx.data.finding.fingerprint === f.fingerprint ? cx.data : undefined;
    const advice = c?.advice;
    const candidates = taskAgents(agents.data ?? []);
    const chosen = candidates.find((a) => a.id === agentId) ?? candidates[0];
    const folder = !report.projectId;
    const noFp = !f.fingerprint;
    const taskLabel = !folder && chosen ? `Make a task for ${chosen.name}` : 'Make a task';
    const title = findingTitle(f);
    const sevColor = f.severity === 'critical' ? t.danger : tone[severityTone(f.severity)].fg;
    return (
      <aside mix={paneCss} aria-label="Finding" data-testid="finding-pane" data-fingerprint={f.fingerprint}>
        <div class="inner">
          <div class="head">
            <span class="sev" style={{ color: sevColor }} data-testid="finding-pane-severity">
              {severityLabel(f.severity)} · {engine}
            </span>
            <h2 data-testid="finding-pane-title">{title}</h2>
            {advice?.meaning ? (
              <p class="meaning" data-testid="finding-meaning">
                {advice.meaning}
              </p>
            ) : null}
          </div>
          <div class="body">
            {noFp ? <p class="note">This finding is from a scan made before findings had fingerprints: scan again to see the code around it.</p> : null}
            <ErrorNote error={cx.error} />
            {!noFp && !c && cx.loading ? <p class="note">Reading the file…</p> : null}
            {c?.changed && c.lines.length > 0 ? <p class="note">The file changed since the scan: these are its lines now.</p> : null}
            {c && c.lines.length > 0 ? <CodeLines lines={c.lines} label={`The code around it, in ${f.file ?? 'the file'}`} testId="finding-lines" /> : null}
            {c?.note ? (
              <p class="note" data-testid="finding-note">
                {c.note}
              </p>
            ) : null}
            {advice && advice.steps.length > 0 ? (
              <div style={{ display: 'grid', gap: t.s2 }}>
                <span class="label">What to do</span>
                <ol data-testid="finding-steps">
                  {advice.steps.map((st, i) => (
                    <li key={i}>{st}</li>
                  ))}
                </ol>
              </div>
            ) : null}
            {advice && (advice.refs ?? []).length > 0 ? (
              <div class="refs" data-testid="finding-refs">
                <span class="label">Read more</span>
                {(advice.refs ?? []).map((r) => {
                  const url = refUrl(r);
                  return url ? (
                    <a key={r} href={url} target="_blank" rel="noreferrer" data-rmx-document title={r}>
                      {refLabel(r)}
                    </a>
                  ) : (
                    <span key={r} title={r}>
                      {refLabel(r)}
                    </span>
                  );
                })}
              </div>
            ) : null}
            {made && made.at === at ? (
              <Notice tone="ok" testId="finding-task-made">
                Made the task <a href={routes.task.href({ id: made.id })}>{made.title}</a>.
              </Notice>
            ) : null}
            <ErrorNote error={taskAt === at ? taskAct.error : undefined} />
            {folder ? (
              <p class="note" data-testid="finding-task-folder">
                A folder scan belongs to no project, so no task can be made from it.
              </p>
            ) : null}
          </div>
          <div class="actions">
            {!folder && candidates.length > 1 ? (
              <Select
                label="Who the task is for"
                testId="finding-task-agent"
                value={chosen?.id ?? ''}
                options={candidates.map((a) => ({ value: a.id, label: a.name }))}
                onChange={(v) => {
                  agentId = v;
                  void handle.update();
                }}
                style={{ width: 'auto', maxWidth: '180px' }}
              />
            ) : null}
            <Button
              variant="primary"
              data-testid="finding-task"
              disabled={folder || noFp || busy || taskAct.busy !== '' || (made?.at === at)}
              title={folder ? 'A task belongs to a project; this scan is of a folder.' : undefined}
              mix={on('click', () => void makeTask(folder ? '' : (chosen?.id ?? '')))}
            >
              {taskAct.busy ? 'Making the task…' : taskLabel}
            </Button>
            <Button data-testid="finding-open-file" disabled={!f.file || noFp} mix={on('click', () => void openFile())}>
              Open the file
            </Button>
            <span class="end">
              <Menu
                label="Dismiss the finding"
                items={f.engine === 'secrets' ? DISMISS_SECRET : DISMISS_OTHER}
                placement="bottom-end"
                onSelect={(name) => void handle.props.onDismiss(f, name as DismissReason)}
                trigger={(mix) => (
                  <button type="button" mix={[...buttonStyle('ghost'), mix]} data-testid="finding-dismiss" disabled={noFp || busy}>
                    {f.engine === 'secrets' ? 'Not a real key' : 'Dismiss'}
                  </button>
                )}
              />
            </span>
          </div>
        </div>
        <Dialog open={fileOpen} title={f.file ?? 'The file'} wide onClose={closeFile}>
          <p class="note" style={{ margin: '0', color: t.textMuted }}>
            Read-only, as the scan read it: {placeText(f)}. Secrets are hidden.
          </p>
          <ErrorNote error={fileAct.error} />
          {fileAct.busy ? <p style={{ margin: '0', color: t.textMuted }}>Reading the file…</p> : null}
          {file && file.lines.length > 0 ? <CodeLines lines={file.lines} label={`The whole of ${f.file ?? 'the file'}`} testId="file-lines" /> : null}
          {file?.note ? (
            <p style={{ margin: '0', color: t.textMuted }} data-testid="file-note">
              {file.note}
            </p>
          ) : null}
          {file?.more ? (
            <p style={{ margin: '0', color: t.textMuted }} data-testid="file-more">
              The file is longer: these are its first {file.lines.length.toLocaleString()} lines.
            </p>
          ) : null}
        </Dialog>
      </aside>
    );
  };
}

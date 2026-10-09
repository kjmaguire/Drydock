// The evals report: how the system performed on the human's own tasks. Headline metrics as tiles (a zero denominator is
// n/a, never 0%), then the core's Markdown report rendered inert. Export writes a local file only.

import { css, on, type Handle } from 'remix/component';
import { Download, FolderOpen, Gauge, RefreshCw } from 'lucide';
import type { EvalsExportResult, Ratio } from '../../api/extTypes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { card, Page, Section } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { Meter } from '../budgets/meter.tsx';
import { evalsOptions, ratioRate, ratioText, WINDOWS } from './evals.ts';

const introCss = css({ color: t.textMuted, fontSize: t.textMd, maxWidth: '820px' });
const toolbarCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s4,
  flexWrap: 'wrap',
  '& .window': { display: 'inline-flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, color: t.textMuted },
  '& .window select': { width: 'auto' },
});
const tilesCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(210px, 1fr))',
  gap: t.s3,
  margin: '0',
});
const tileCss = css({
  display: 'grid',
  gap: t.s2,
  alignContent: 'start',
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& dt': { color: t.textMuted, fontSize: t.textSm },
  '& dd': { margin: '0', fontSize: t.textXl, fontWeight: '600', fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.01em' },
});
const reportCss = css({ padding: `${t.s4} ${t.s5}` });

export function EvalsScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let days = 0;
  let thisProject = true;
  let exported: EvalsExportResult | undefined;
  const opts = () => evalsOptions(s.workspace.projectId, days, thisProject);
  const q = query(handle, {
    load: () => s.client.evalsReport(opts()),
    when: (e) => e.type.startsWith('task.') || e.type.startsWith('routing.') || e.type.startsWith('workflow.recover'),
    key: () => [s.workspace.projectId, days, thisProject],
    enabled: () => !!s.workspace.projectId,
  });

  const exportFile = async () => {
    const r = await act.run('export', () => s.client.evalsExport(opts()));
    if (r) {
      exported = r;
      void handle.update();
    }
  };

  const ratioTile = (label: string, r: Ratio | undefined, testId?: string) => {
    const rate = ratioRate(r);
    return (
      <div mix={tileCss}>
        <dt>{label}</dt>
        <dd data-testid={testId}>{ratioText(r)}</dd>
        {rate !== undefined ? <Meter value={rate} tone={rate >= 0.5 ? 'ok' : 'warn'} label={`${label}: ${ratioText(r)}`} /> : null}
      </div>
    );
  };

  return () => {
    const projectId = s.workspace.projectId;
    const project = s.workspace.project;
    const rep = q.data?.report;
    const quality = rep?.quality;
    return (
      <Page
        title="Evals report"
        icon={Gauge}
        subtitle={project ? (thisProject ? project.name : 'All projects') : undefined}
        actions={
          projectId ? (
            <>
              <Button variant="ghost" icon={RefreshCw} mix={on('click', () => q.reload())}>
                Refresh
              </Button>
              <Button icon={Download} disabled={act.busy !== ''} mix={on('click', () => void exportFile())}>
                Export to a local file
              </Button>
            </>
          ) : undefined
        }
      >
        <p mix={introCss}>
          How the system performed on your own tasks. Rates over a zero denominator show n/a, because no measurement is not a measured 0. These are counts and medians
          from your history, not claims of significance.
        </p>
        {!projectId ? (
          s.workspace.projects.data === undefined ? (
            <Loading />
          ) : (
            <Empty title="No project selected" icon={FolderOpen}>
              <p>The report is built from a project&apos;s task history. Choose or create a project first.</p>
            </Empty>
          )
        ) : (
          <>
            <div mix={toolbarCss}>
              <label class="window">
                Window
                <select
                  aria-label="Window"
                  value={String(days)}
                  mix={on('change', (e) => {
                    days = Number(e.currentTarget.value);
                    void handle.update();
                  })}
                >
                  {WINDOWS.map(([l, d]) => (
                    <option key={d} value={String(d)} selected={d === days}>
                      {l}
                    </option>
                  ))}
                </select>
              </label>
              <Checkbox
                label="This project only"
                checked={thisProject}
                onChange={(v) => {
                  thisProject = v;
                  void handle.update();
                }}
              />
            </div>
            <ErrorNote error={q.error ?? act.error} what="Evals" />
            {exported ? (
              <Notice tone="ok" role="status" testId="evals-exported">
                Wrote <code>{exported.path}</code> ({exported.bytes} bytes, {exported.tasks} tasks, sha256 <code>{exported.sha256.slice(0, 16)}</code>). It stays on
                this machine.
              </Notice>
            ) : null}
            {quality ? (
              <dl mix={tilesCss} aria-label="Headline" data-testid="evals-headline">
                <div mix={tileCss}>
                  <dt>Tasks started / accepted</dt>
                  <dd>
                    {quality.started} / {quality.accepted}
                  </dd>
                </div>
                {ratioTile('Completion', quality.completion, 'ratio-completion')}
                {ratioTile('First check test pass', quality.firstCheckTestPass)}
                {ratioTile('Final test pass (accepted)', quality.finalTestPass)}
                {rep?.cheap ? ratioTile('Cheap-worker share of accepted', rep.cheap.cheapWorker) : null}
                {rep?.routing ? ratioTile('Routing decisions accepted without escalation', rep.routing.good) : null}
                <div mix={tileCss}>
                  <dt>Active / failed / cancelled</dt>
                  <dd>
                    {quality.active} / {quality.failed} / {quality.cancelled}
                  </dd>
                </div>
              </dl>
            ) : null}
            {q.loading && !q.data ? <Loading label="Building the report..." /> : null}
            {q.data ? (
              <Section title="Full report" label="Full report" testId="evals-markdown">
                <div mix={[card, reportCss]}>
                  <Markdown source={q.data.markdown} maxChars={200000} />
                </div>
              </Section>
            ) : null}
          </>
        )}
      </Page>
    );
  };
}

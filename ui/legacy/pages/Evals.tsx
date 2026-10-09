import { useState } from 'react';
import type { EvalsExportResult, EvalsOptions, Ratio } from '../api/extTypes';
import { Markdown } from '../lib/Markdown';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { ErrorNote, Page } from '../components/common';

/** A ratio over a denominator. A zero denominator is "n/a", never 0%: a missing measurement is not a measured zero. */
export function ratioText(r: Ratio | undefined | null): string {
  if (!r || r.den === 0 || r.rate === null || r.rate === undefined) return 'n/a';
  return `${r.num} of ${r.den} (${Math.round(r.rate * 100)}%)`;
}

const WINDOWS: [string, number][] = [
  ['All time', 0],
  ['Last 7 days', 7],
  ['Last 30 days', 30],
];

export default function EvalsView({ projectId }: { projectId: string }) {
  const client = useClient();
  const [days, setDays] = useState(0);
  const [thisProject, setThisProject] = useState(true);
  const act = useAction();
  const [exported, setExported] = useState<EvalsExportResult | undefined>();
  const opts: EvalsOptions = { projectId: thisProject ? projectId : undefined, sinceMs: days > 0 ? Date.now() - days * 86_400_000 : undefined };
  const q = useLive(() => client.evalsReport(opts), [projectId, days, thisProject], (e) => e.type.startsWith('task.') || e.type.startsWith('routing.') || e.type.startsWith('workflow.recover'));
  const rep = q.data?.report;
  const quality = rep?.quality;
  return (
    <Page title="Evals report">
      <p className="muted">
        How the system performed on your own tasks. Rates over a zero denominator show n/a, because no measurement is not a measured 0. These are counts and medians from
        your history, not claims of significance.
      </p>
      <div className="form-actions">
        <label>
          Window
          <select aria-label="Window" value={days} onChange={(e) => setDays(Number(e.target.value))}>
            {WINDOWS.map(([l, d]) => (
              <option key={d} value={d}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <label className="inline">
          <input type="checkbox" checked={thisProject} onChange={(e) => setThisProject(e.target.checked)} /> This project only
        </label>
        <button onClick={q.reload}>Refresh</button>
        <button
          disabled={act.busy !== ''}
          onClick={async () => {
            const r = await act.run('export', () => client.evalsExport(opts));
            if (r) setExported(r);
          }}
        >
          Export to a local file
        </button>
      </div>
      <ErrorNote error={q.error ?? act.error} what="Evals" />
      {exported ? (
        <p role="status" data-testid="evals-exported">
          Wrote <code>{exported.path}</code> ({exported.bytes} bytes, {exported.tasks} tasks, sha256 <code>{exported.sha256.slice(0, 16)}</code>). It stays on this machine.
        </p>
      ) : null}
      {quality ? (
        <table className="table" aria-label="Headline" data-testid="evals-headline">
          <tbody>
            <tr>
              <th scope="row">Tasks started / accepted</th>
              <td>
                {quality.started} / {quality.accepted}
              </td>
            </tr>
            <tr>
              <th scope="row">Completion</th>
              <td data-testid="ratio-completion">{ratioText(quality.completion)}</td>
            </tr>
            <tr>
              <th scope="row">First check test pass</th>
              <td>{ratioText(quality.firstCheckTestPass)}</td>
            </tr>
            <tr>
              <th scope="row">Final test pass (accepted)</th>
              <td>{ratioText(quality.finalTestPass)}</td>
            </tr>
            {rep?.cheap ? (
              <tr>
                <th scope="row">Cheap-worker share of accepted</th>
                <td>{ratioText(rep.cheap.cheapWorker)}</td>
              </tr>
            ) : null}
            {rep?.routing ? (
              <tr>
                <th scope="row">Routing decisions accepted without escalation</th>
                <td>{ratioText(rep.routing.good)}</td>
              </tr>
            ) : null}
          </tbody>
        </table>
      ) : null}
      {q.loading && !q.data ? <p className="muted">Building the report...</p> : null}
      {q.data ? (
        <section aria-label="Full report" data-testid="evals-markdown">
          <Markdown source={q.data.markdown} maxChars={200000} />
        </section>
      ) : null}
    </Page>
  );
}

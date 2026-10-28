// Crash reports (internal/crashreport, review 2026-10-27): when a run of Drydock died of something nothing could recover, the
// next start kept what Go printed, with secrets redacted. They are listed here, they go into every support bundle (scrubbed
// again), and they never leave this computer on their own. The group is drawn only when there is a report.
import { css, on, type Handle } from 'remix/component';
import { TriangleAlert } from 'lucide';
import { formatDateTime } from '../../lib/format.ts';
import { starts } from '../../state/live.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, mono } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { Group, panelPad } from '../settings/settings-kit.tsx';

export function CrashReports(handle: Handle) {
  const s = services(handle);
  const list = query(handle, { load: () => s.client.crashList(), when: (e) => starts(e, 'app.crashed') });
  const act = action(handle);

  const remove = async (name: string) => {
    if (await act.run(name, () => s.client.crashDelete(name))) await list.reload();
  };

  return () => {
    const reports = list.data?.reports ?? [];
    if (list.error) return <ErrorNote error={list.error} what="Crash reports" />;
    if (reports.length === 0) return null;
    return (
      <Group
        title="Crash reports"
        icon={TriangleAlert}
        testId="crash-reports"
        description="Drydock stopped unexpectedly. What it printed is kept here, with secrets removed, and goes into the support bundle you create."
      >
        <div mix={panelPad}>
          <ul mix={listCss} aria-label="Crash reports">
            {reports.map((r) => (
              <li key={r.name} mix={rowCss} data-testid="crash-report">
                <div mix={textCss}>
                  <strong>{r.time ? formatDateTime(r.time) : r.name}</strong>
                  <span mix={[mono, faint]}>{r.summary}</span>
                </div>
                <Button size="sm" disabled={act.busy !== ''} aria-label={`Delete the crash report of ${r.time ? formatDateTime(r.time) : r.name}`} mix={on('click', () => void remove(r.name))}>
                  Delete
                </Button>
              </li>
            ))}
          </ul>
          <ErrorNote error={act.error} what="Delete" />
        </div>
      </Group>
    );
  };
}

const listCss = css({ margin: '0', padding: '0', listStyle: 'none' });
const rowCss = css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: t.s3, padding: `${t.s2} 0` });
const textCss = css({ display: 'flex', flexDirection: 'column', gap: t.s1, minWidth: '0', '& > span': { overflowWrap: 'anywhere' } });

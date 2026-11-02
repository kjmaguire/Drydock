// Crash reports (internal/crashreport, review 2026-10-27): when a run of Drydock died of something nothing could recover, the
// next start kept what Go printed, with secrets redacted. They are listed here, they go into every support bundle (scrubbed
// again), and they never leave this computer on their own. The group is drawn only when there is a report. Show reads one
// report's redacted text (crash.read) into a <pre> as text, never as markup, with Copy; Report this goes to the bundle; Delete is
// in the row's menu.
import { css, on, type Handle } from 'remix/component';
import { Copy, FileArchive, TriangleAlert } from 'lucide';
import { formatDateTime } from '../../lib/format.ts';
import { starts } from '../../state/live.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, mono } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { MoreMenu } from '../../ui/menu.tsx';
import { t } from '../../ui/tokens.ts';
import { Group, panelPad } from '../settings/settings-kit.tsx';

/** `onReport` takes the person to the support bundle, which carries every report. */
export function CrashReports(handle: Handle<{ onReport?: () => void }>) {
  const s = services(handle);
  const list = query(handle, { load: () => s.client.crashList(), when: (e) => starts(e, 'app.crashed') });
  const act = action(handle);
  /** The texts read so far, by report name, and the reports open now. */
  const texts = new Map<string, string>();
  const shown = new Set<string>();
  let copied = '';

  const remove = async (name: string) => {
    if (await act.run(name, () => s.client.crashDelete(name))) {
      shown.delete(name);
      texts.delete(name);
      await list.reload();
    }
  };
  const toggle = async (name: string) => {
    if (shown.has(name)) {
      shown.delete(name);
      void handle.update();
      return;
    }
    if (!texts.has(name)) {
      const r = await act.run(`read:${name}`, () => s.client.crashRead(name));
      if (!r) return;
      texts.set(name, r.text);
    }
    shown.add(name);
    void handle.update();
  };
  const copy = async (name: string) => {
    try {
      await navigator.clipboard.writeText(texts.get(name) ?? '');
      copied = name;
      void handle.update();
    } catch {
      /* the clipboard can be refused; the text is on the page to select */
    }
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
            {reports.map((r) => {
              const when = r.time ? formatDateTime(r.time) : r.name;
              const open = shown.has(r.name);
              return (
                <li key={r.name} data-testid="crash-report" data-open={String(open)}>
                  <div mix={rowCss}>
                    <div mix={textCss}>
                      <strong>{when}</strong>
                      <span mix={[mono, faint]}>{r.summary}</span>
                    </div>
                    <div mix={actionsCss}>
                      <Button size="sm" aria-expanded={open ? 'true' : 'false'} disabled={act.busy !== ''} aria-label={`${open ? 'Hide' : 'Show'} the crash report of ${when}`} mix={on('click', () => void toggle(r.name))}>
                        {act.busy === `read:${r.name}` ? 'Loading...' : open ? 'Hide' : 'Show'}
                      </Button>
                      {handle.props.onReport ? (
                        <Button size="sm" icon={FileArchive} mix={on('click', () => handle.props.onReport?.())}>
                          Report this
                        </Button>
                      ) : null}
                      <MoreMenu
                        label={`More for the crash report of ${when}`}
                        items={[{ name: 'delete', label: 'Delete', danger: true, disabled: act.busy !== '' }]}
                        onSelect={(name) => name === 'delete' && void remove(r.name)}
                        testId="crash-more"
                      />
                    </div>
                  </div>
                  {open ? (
                    <div mix={reportCss} data-testid="crash-text">
                      <pre mix={mono}>{texts.get(r.name) ?? ''}</pre>
                      <div>
                        <Button size="sm" icon={Copy} mix={on('click', () => void copy(r.name))}>
                          {copied === r.name ? 'Copied' : 'Copy'}
                        </Button>
                      </div>
                    </div>
                  ) : null}
                </li>
              );
            })}
          </ul>
          <ErrorNote error={act.error} what="Crash report" />
        </div>
      </Group>
    );
  };
}

const listCss = css({ margin: '0', padding: '0', listStyle: 'none' });
const rowCss = css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: t.s3, padding: `${t.s2} 0` });
const actionsCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flex: 'none' });
const reportCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `0 0 ${t.s3}`,
  '& pre': { margin: '0', maxHeight: '360px', overflow: 'auto', padding: t.s3, borderRadius: t.rMd, background: t.bgApp, fontSize: t.textXs, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
});
const textCss = css({ display: 'flex', flexDirection: 'column', gap: t.s1, minWidth: '0', '& > span': { overflowWrap: 'anywhere' } });

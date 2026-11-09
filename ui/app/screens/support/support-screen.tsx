// Support bundle: a local zip of scrubbed diagnostics for a bug report. The preview (the file list and the scrub report, names
// and counts only) is made when the page opens and again shortly after a choice changes (a preview builds the whole bundle in
// memory, so it waits for the typing to stop); the bundle is then created on this computer. Including one run is opt-in behind an acknowledgement. Drydock
// never uploads a bundle. Works in every license state and with no project.
//
// The run to include is picked from the newest runs that failed or were stopped part way (supportbundle.recentRuns, X56), or typed
// with "Another run id..."; Report this on a run page, a failed task or an Inbox item opens /support?run=<id>, which picks that run
// and previews with it at once. The acknowledgement before a bundle with a run is created stays.
//
// Creating never writes over a file by itself (audit 2026-10-16, L39): when a file is already at the destination the core writes
// nothing and answers that it exists; the page asks "Replace it?" and sends the request again with overwrite only after the person
// says yes. A bundle that was written is shown with what was done to it: the scrub report, the files left out and the files in it.

import { css, on, ref, type Handle } from 'remix/component';
import { FileArchive, FileSearch, LifeBuoy, ShieldCheck } from 'lucide';
import type { BundleCreated, BundleExists, BundleFile, BundlePreview, BundleReport } from '../../api/pkgTypes.ts';
import type { RecentRun } from '../../api/pkgClient.ts';
import { agoWords, formatBytes } from '../../lib/format.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, mono } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { Checkbox, errorAttrs, Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Select, type SelectOption } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { RuleCounts } from '../data/rule-counts.tsx';
import { confirmRow, Done, Group, panelPad, plainList, settingsColumn, subhead, tableWrap, Warn } from '../settings/settings-kit.tsx';
import { CrashReports } from './crash-reports.tsx';
import { bundleKey, bundleOpts, identityText, scrubbedFiles, type BundleOpts } from './support.ts';
import { plural } from '../../lib/format.ts';
import { ChooseSave, SavedFile } from '../shared/file-actions.tsx';
import { folderRow } from '../shared/choose-folder.tsx';

const formCss = css({ display: 'grid', gap: t.s4 });
const neverCss = css({
  display: 'flex',
  gap: t.s3,
  alignItems: 'flex-start',
  margin: '0',
  padding: `${t.s3} ${t.s4}`,
  borderRadius: t.rLg,
  border: `1px solid color-mix(in srgb, ${t.success} 30%, transparent)`,
  background: t.successSoft,
  fontSize: t.textMd,
  lineHeight: '1.55',
  '& > svg': { color: t.success, marginTop: '2px' },
});
// the question "Replace it?": the same inline two-step confirm as the other pages (a danger-tinted row, the safe answer last)
const askCss = css({
  padding: `${t.s2} ${t.s3}`,
  borderRadius: t.rMd,
  background: t.dangerSoft,
  '& code': { overflowWrap: 'anywhere' },
});
const previewCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& > p': { margin: '0' },
});

/**
 * The scrub report as the core gives it: names and counts only. No scanned text is ever shown here. The report of a preview and
 * the report of a bundle that was written are both on the page after a create, so the second has test ids of its own.
 */
export function ScrubReport(handle: Handle<{ r: BundleReport; testId?: string; totalTestId?: string }>) {
  return () => {
    const { r, testId = 'scrub-report', totalTestId = 'scrub-total' } = handle.props;
    const files = r.files ?? [];
    const touched = scrubbedFiles(r);
    return (
      <div data-testid={testId} style={{ display: 'grid', gap: t.s2 }}>
        <h3 mix={subhead}>Scrub report</h3>
        <p style={{ margin: '0' }}>
          <strong data-testid={totalTestId}>{r.totalSecretsRedacted}</strong> secret-like {r.totalSecretsRedacted === 1 ? 'value' : 'values'} redacted across {plural(files.length, 'file')}.
        </p>
        <RuleCounts by={r.byRule} />
        {touched.length > 0 ? (
          <div mix={tableWrap}>
            <table aria-label="Per-file scrub results">
              <thead>
                <tr>
                  <th scope="col">File</th>
                  <th scope="col">Secrets redacted</th>
                  <th scope="col">Identity replaced</th>
                </tr>
              </thead>
              <tbody>
                {touched.map((f) => (
                  <tr key={f.name}>
                    <td>
                      <code>{f.name}</code>
                    </td>
                    <td>{f.secretsRedacted}</td>
                    <td>{identityText(f)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {(r.refused ?? []).length > 0 ? (
          <>
            <p style={{ margin: '0' }}>Left out of the bundle:</p>
            <ul mix={plainList}>
              {(r.refused ?? []).map((x) => (
                <li key={x.name}>
                  <code>{x.name}</code>: {x.reason}
                </li>
              ))}
            </ul>
          </>
        ) : null}
        <p mix={faint} style={{ margin: '0' }}>
          {r.notice}
        </p>
      </div>
    );
  };
}

function FileTable(handle: Handle<{ files: BundleFile[] | null; label?: string; rowTestId?: string }>) {
  return () => (
    <div mix={tableWrap}>
      <table aria-label={handle.props.label ?? 'Files in the support bundle'}>
        <thead>
          <tr>
            <th scope="col">File</th>
            <th scope="col">Size</th>
          </tr>
        </thead>
        <tbody>
          {(handle.props.files ?? []).map((f) => (
            <tr key={f.name} data-testid={handle.props.rowTestId ?? 'bundle-file'}>
              <td>
                <code>{f.name}</code>
              </td>
              <td style={{ whiteSpace: 'nowrap' }}>{formatBytes(f.size)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** How long the page waits after a choice changes before it previews again. */
export const PREVIEW_DELAY_MS = 600;

/** The picker's choice that shows the run id field. */
export const OTHER_RUN = 'other';

/** The picker's options: no run, the recent failed or paused runs by task, then a typed id. */
export function runOptions(runs: readonly RecentRun[], now = Date.now()): SelectOption[] {
  return [
    { value: '', label: 'No run' },
    ...runs.map((r) => ({
      value: r.runId,
      label: `${r.title || r.runId} (${r.status === 'PAUSED' ? 'stopped part way' : 'failed'})`,
      hint: [r.project, agoWords(r.updatedAt, now)].filter(Boolean).join(', '),
    })),
    { value: OTHER_RUN, label: 'Another run id...' },
  ];
}

/** `run` comes from the address (/support?run=<id>, Report this): that run is picked and the first preview includes it. */
export function SupportScreen(handle: Handle<{ run?: string }>) {
  const s = services(handle);
  const act = action(handle);
  let events = '';
  let eventsError: string | undefined;
  let run = (handle.props.run ?? '').trim();
  /** The recent failed or paused runs; none (an older core, or a failed read) leaves only the id field. */
  let recent: RecentRun[] = [];
  /** The person chose "Another run id...", or the address named a run the list does not have. */
  let other = false;
  void Promise.resolve()
    .then(() => s.client.supportbundleRecentRuns())
    .then((r) => {
      if (handle.signal.aborted) return;
      recent = r.runs;
      if (run && !recent.some((x) => x.runId === run)) other = true;
      void handle.update();
    })
    .catch(() => {
      /* no list: the id field stays, as before */
    });
  let ack = false;
  let dest = '';
  // the choices a preview was made for travel with it: the bundle is made from exactly them, and only while the page still shows them
  let preview: (BundlePreview & { key: string; opts: BundleOpts }) | undefined;
  let created: BundleCreated | undefined;
  /** A file is already at the destination: nothing was written, and the person is asked whether it may be replaced. */
  let exists: BundleExists | undefined;
  /** The person answered "Keep it" for this file. */
  let kept: string | undefined;
  let root: HTMLElement | undefined;

  // what was asked of the core by the choices on the page is not the question any more once they change
  const forget = () => {
    exists = undefined;
    kept = undefined;
  };
  // the person is told under the events field and the keyboard goes to it
  const refuseEvents = (error: string) => {
    eventsError = error;
    previewError = undefined;
    act.clear();
    handle.queueTask(() => root?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
  };
  // the question goes away with the button that answered it: the keyboard goes back to the button that asked it
  const focusCreate = () => handle.queueTask(() => root?.querySelector<HTMLElement>('[data-testid="bundle-create"]')?.focus());

  /**
   * Only the newest preview asked for is shown: an older one that answers late is dropped, with its error. Previews can overlap
   * (one starts while an earlier one still runs), so they keep their own busy and error, set and cleared only by the newest.
   */
  let asked = 0;
  /** The number of the preview still running (0: none). */
  let previewing = 0;
  let previewError: Error | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  // `auto`: made by the page itself (on opening, after a change); a bad events field is then said under it without taking the keyboard
  const runPreview = async (auto = false) => {
    clearTimeout(timer);
    const o = bundleOpts(events, run);
    if (!o.ok) {
      if (!auto) return refuseEvents(o.error);
      eventsError = o.error;
      return void handle.update();
    }
    eventsError = undefined;
    const key = bundleKey(events, run);
    created = undefined;
    forget();
    ack = false;
    const n = ++asked;
    previewing = n;
    previewError = undefined;
    act.clear();
    let r: BundlePreview | undefined;
    let err: Error | undefined;
    try {
      r = await s.client.supportbundlePreview(o.opts);
    } catch (e) {
      err = e instanceof Error ? e : new Error(String(e));
    }
    if (n !== asked || handle.signal.aborted) return;
    previewing = 0;
    previewError = err;
    if (r) preview = { ...r, key, opts: o.opts };
    void handle.update();
  };
  const later = () => {
    clearTimeout(timer);
    timer = setTimeout(() => void runPreview(true), PREVIEW_DELAY_MS);
  };
  // the preview is there when the page opens
  handle.queueTask(() => void runPreview(true));
  /** Report this (a crash report): the bundle carries every report, so the keyboard goes to Create, or to the build when there is none yet. */
  const toBundle = () => {
    const target = root?.querySelector<HTMLElement>('[data-testid="bundle-create"]') ?? root?.querySelector<HTMLElement>('[data-testid="bundle-build-body"]');
    target?.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
    target?.focus();
  };
  const runCreate = async (opts: BundleOpts, overwrite = false) => {
    created = undefined;
    previewError = undefined;
    forget();
    // overwrite goes only with the answer "Yes, replace it": the first request never carries it
    const r = await act.run('create', () =>
      s.client.supportbundleCreate({ ...opts, dest: dest.trim() || undefined, acknowledgeRun: !!opts.includeRun && ack, ...(overwrite ? { overwrite: true } : {}) }),
    );
    if (r) {
      if ('exists' in r) exists = r;
      else created = r;
    }
    void handle.update();
    if (overwrite) focusCreate();
  };

  return () => {
    const includeRun = run.trim() || undefined;
    const shown = preview;
    const fresh = shown?.key === bundleKey(events, run);
    const busy = act.busy !== '' || previewing !== 0;
    return (
      <Page title="Support bundle" icon={LifeBuoy} subtitle="A zip of scrubbed diagnostics for a bug report" guide="support-bundle">
        <div mix={[settingsColumn, ref((n: Element) => (root = n as HTMLElement))]}>
          <p mix={neverCss} data-testid="bundle-never-uploaded">
            <Icon icon={ShieldCheck} size={16} />
            <span>
              <strong>Drydock never uploads a support bundle.</strong> Creating one writes a file on this computer; you decide whether and where to send it. Support
              bundles work in every license state.
            </span>
          </p>

          <CrashReports onReport={toBundle} />

          <Group
            title="Build a bundle"
            icon={FileSearch}
            testId="bundle-build"
            description="The preview below is made for these choices: nothing is written until you create the bundle, and you see every file and what was scrubbed."
          >
            <div mix={panelPad} tabIndex={-1} data-testid="bundle-build-body">
              <div mix={formCss}>
                {recent.length > 0 ? (
                  <Field label="Run to include (optional). Adds that run's events with payloads and its raw output.">
                    {(id) => (
                      <Select
                        id={id}
                        testId="bundle-run-pick"
                        value={other ? OTHER_RUN : recent.some((x) => x.runId === run.trim()) ? run.trim() : ''}
                        options={runOptions(recent)}
                        onChange={(v) => {
                          other = v === OTHER_RUN;
                          if (!other) {
                            run = v;
                            later();
                          }
                          ack = false;
                          forget();
                          void handle.update();
                        }}
                      />
                    )}
                  </Field>
                ) : null}
                {recent.length === 0 || other ? (
                  <Field label="Include one run (optional; a run id). Adds that run's events with payloads and its raw output.">
                    {(id) => (
                      <input
                        id={id}
                        value={run}
                        spellcheck={false}
                        placeholder="run_..."
                        mix={[mono, on('input', (e) => ((run = e.currentTarget.value), (ack = false), forget(), later(), void handle.update()))]}
                      />
                    )}
                  </Field>
                ) : null}
                <Disclosure summary="More options" open={events !== '' || !!eventsError} testId="bundle-more">
                  <Field label="Recent events to list (empty for the default of 500, at most 5000)" error={eventsError}>
                    {(id) => (
                      <input
                        id={id}
                        inputMode="numeric"
                        value={events}
                        placeholder="500"
                        {...errorAttrs(id, eventsError)}
                        mix={on('input', (e) => ((events = e.currentTarget.value), (eventsError = undefined), (ack = false), forget(), later(), void handle.update()))}
                      />
                    )}
                  </Field>
                </Disclosure>
                <div mix={formActions}>
                  <Button icon={FileSearch} disabled={busy} mix={on('click', () => void runPreview())}>
                    {previewing !== 0 ? 'Building the preview...' : 'Preview again'}
                  </Button>
                </div>
              </div>
            </div>
            <ErrorNote error={previewError ?? act.error} what="Support bundle" />
          </Group>

          {shown ? (
            <Group title="Preview" icon={FileArchive} description="What the bundle would contain. Names and counts only: no scanned text is ever shown here.">
              <div mix={previewCss} data-testid="bundle-preview" data-fresh={String(fresh)}>
                <p mix={faint}>Nothing has been written yet. This is what the bundle would contain.</p>
                {!fresh ? <Warn>This preview is for different choices. Preview again before creating.</Warn> : null}
                {shown.warning ? <Warn testId="bundle-run-warning">{shown.warning}</Warn> : null}
                <h3 mix={subhead}>Files</h3>
                <FileTable files={shown.files} />
                <ScrubReport r={shown.report} />
              </div>
              {fresh ? (
                <div mix={panelPad}>
                  <div mix={formCss}>
                    {includeRun ? (
                      <Checkbox
                        label={`I have read the scrub report above for run ${includeRun} and I understand its payloads and raw output can contain source code.`}
                        checked={ack}
                        onChange={(c) => ((ack = c), forget(), void handle.update())}
                      />
                    ) : null}
                    <Field label="Save to (optional full path; empty uses the support folder in the Drydock data folder)">
                      {(id) => (
                        <div mix={folderRow}>
                          <input id={id} value={dest} spellcheck={false} mix={[mono, on('input', (e) => ((dest = e.currentTarget.value), forget(), void handle.update()))]} />
                          <ChooseSave kind="support-bundle" dir={dest} testId="bundle-choose-save" onPick={(p) => ((dest = p), forget(), void handle.update())} />
                        </div>
                      )}
                    </Field>
                    <div mix={formActions}>
                      <Button variant="primary" icon={FileArchive} data-testid="bundle-create" disabled={busy || (!!includeRun && !ack)} mix={on('click', () => void runCreate(shown.opts))}>
                        {act.busy === 'create' ? 'Creating...' : 'Create the bundle'}
                      </Button>
                    </div>
                    {exists ? (
                      <div mix={[confirmRow, askCss]} role="alert" data-testid="bundle-exists">
                        <span>
                          A file is already there: <code data-testid="bundle-exists-path">{exists.path}</code> ({formatBytes(exists.size)}). Replace it?
                        </span>
                        <Button size="sm" variant="danger" disabled={busy} mix={on('click', () => void runCreate(shown.opts, true))}>
                          Yes, replace it
                        </Button>
                        <Button size="sm" disabled={busy} mix={on('click', () => ((kept = exists?.path), (exists = undefined), void handle.update(), focusCreate()))}>
                          Keep it
                        </Button>
                      </div>
                    ) : null}
                    {kept ? (
                      <p mix={faint} role="status" data-testid="bundle-kept" style={{ margin: '0' }}>
                        Nothing was written. The file at <code>{kept}</code> was left as it is. Choose another name in Save to if you want to keep both.
                      </p>
                    ) : null}
                  </div>
                </div>
              ) : null}
              {created ? (
                <>
                  <Done testId="bundle-created">
                    <span>
                      Created <code data-testid="bundle-path">{created.path}</code> ({formatBytes(created.size)}, {plural((created.files ?? []).length, 'file')}). It has not been
                      uploaded anywhere.
                      {created.replaced ? (
                        <>
                          {' '}
                          <strong data-testid="bundle-replaced">It replaced the earlier file.</strong>
                        </>
                      ) : null}
                    </span>
                  </Done>
                  <SavedFile path={created.path} testId="bundle-saved" />
                  <div mix={previewCss} data-testid="bundle-written">
                    <h3 mix={subhead}>What was written</h3>
                    <FileTable files={created.files} label="Files written to the bundle" rowTestId="bundle-written-file" />
                    <ScrubReport r={created.report} testId="written-scrub-report" totalTestId="written-scrub-total" />
                  </div>
                </>
              ) : null}
            </Group>
          ) : null}
        </div>
      </Page>
    );
  };
}

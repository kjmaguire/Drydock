// Support bundle: a local zip of scrubbed diagnostics for a bug report. Preview first (the file list and the scrub report,
// names and counts only), then create it on this computer. Including one run is opt-in behind an acknowledgement. Drydock
// never uploads a bundle. Works in every license state and with no project.

import { css, on, type Handle } from 'remix/component';
import { FileArchive, FileSearch, LifeBuoy, ShieldCheck } from 'lucide';
import type { BundleCreated, BundleFile, BundlePreview, BundleReport } from '../../api/pkgTypes.ts';
import { formatBytes } from '../../lib/format.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, mono } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { RuleCounts } from '../data/rule-counts.tsx';
import { Done, Group, intro, panelPad, plainList, settingsColumn, subhead, tableWrap, Warn } from '../settings/settings-kit.tsx';
import { bundleKey, bundleOpts, identityText, scrubbedFiles } from './support.ts';

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

/** The scrub report as the core gives it: names and counts only. No scanned text is ever shown here. */
export function ScrubReport(handle: Handle<{ r: BundleReport }>) {
  return () => {
    const r = handle.props.r;
    const files = r.files ?? [];
    const touched = scrubbedFiles(r);
    return (
      <div data-testid="scrub-report" style={{ display: 'grid', gap: 'var(--space-2)' }}>
        <h3 mix={subhead}>Scrub report</h3>
        <p style={{ margin: '0' }}>
          <strong data-testid="scrub-total">{r.totalSecretsRedacted}</strong> secret-like value(s) redacted across {files.length} file(s).
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

function FileTable(handle: Handle<{ files: BundleFile[] | null }>) {
  return () => (
    <div mix={tableWrap}>
      <table aria-label="Files in the support bundle">
        <thead>
          <tr>
            <th scope="col">File</th>
            <th scope="col">Size</th>
          </tr>
        </thead>
        <tbody>
          {(handle.props.files ?? []).map((f) => (
            <tr key={f.name} data-testid="bundle-file">
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

export function SupportScreen(handle: Handle) {
  const s = services(handle);
  const act = action(handle);
  let events = '';
  let run = '';
  let ack = false;
  let dest = '';
  let preview: (BundlePreview & { key: string }) | undefined;
  let created: BundleCreated | undefined;

  const runPreview = async () => {
    const opts = bundleOpts(events, run);
    const key = bundleKey(events, run);
    created = undefined;
    ack = false;
    const r = await act.run('preview', () => s.client.supportbundlePreview(opts));
    if (r) preview = { ...r, key };
    void handle.update();
  };
  const runCreate = async () => {
    const opts = bundleOpts(events, run);
    const r = await act.run('create', () => s.client.supportbundleCreate({ ...opts, dest: dest.trim() || undefined, acknowledgeRun: !!opts.includeRun && ack }));
    if (r) created = r;
    void handle.update();
  };

  return () => {
    const opts = bundleOpts(events, run);
    const fresh = preview?.key === bundleKey(events, run);
    const busy = act.busy !== '';
    return (
      <Page title="Support bundle" icon={LifeBuoy} subtitle="Diagnostics for a bug report">
        <div mix={settingsColumn}>
          <p mix={intro}>
            A support bundle is a zip of diagnostics for a bug report: versions, connection states, settings, recent logs, event types and a scrub report. Secrets are
            redacted and your name, computer name and folders are replaced. Event payloads are dropped (only event types and ids are listed). Scrubbing reduces but does not
            eliminate leakage, so check the file list and report before you send it.
          </p>
          <p mix={neverCss} data-testid="bundle-never-uploaded">
            <Icon icon={ShieldCheck} size={16} />
            <span>
              <strong>Drydock never uploads a support bundle.</strong> Creating one writes a file on this computer; you decide whether and where to send it. Support
              bundles work in every license state.
            </span>
          </p>

          <Group
            title="Build a bundle"
            icon={FileSearch}
            description="Preview first: nothing is written until you create the bundle, and you see every file and what was scrubbed."
          >
            <div mix={panelPad}>
              <div mix={formCss}>
                <Field label="Recent events to list (empty for the default of 500, at most 5000)">
                  {(id) => (
                    <input
                      id={id}
                      inputMode="numeric"
                      value={events}
                      placeholder="500"
                      mix={on('input', (e) => ((events = e.currentTarget.value), (ack = false), void handle.update()))}
                    />
                  )}
                </Field>
                <Field label="Include one run (optional; a run id). Adds that run's events with payloads and its raw output.">
                  {(id) => (
                    <input
                      id={id}
                      value={run}
                      spellcheck={false}
                      placeholder="run_..."
                      mix={[mono, on('input', (e) => ((run = e.currentTarget.value), (ack = false), void handle.update()))]}
                    />
                  )}
                </Field>
                <div mix={formActions}>
                  <Button variant={preview && fresh ? 'secondary' : 'primary'} icon={FileSearch} disabled={busy} mix={on('click', () => void runPreview())}>
                    {act.busy === 'preview' ? 'Building the preview...' : 'Preview the bundle'}
                  </Button>
                </div>
              </div>
            </div>
            <ErrorNote error={act.error} what="Support bundle" />
          </Group>

          {preview ? (
            <Group title="Preview" icon={FileArchive} description="What the bundle would contain. Names and counts only: no scanned text is ever shown here.">
              <div mix={previewCss} data-testid="bundle-preview" data-fresh={String(fresh)}>
                <p mix={faint}>Nothing has been written yet. This is what the bundle would contain.</p>
                {!fresh ? <Warn>This preview is for different choices. Preview again before creating.</Warn> : null}
                {preview.warning ? <Warn testId="bundle-run-warning">{preview.warning}</Warn> : null}
                <h3 mix={subhead}>Files</h3>
                <FileTable files={preview.files} />
                <ScrubReport r={preview.report} />
              </div>
              {fresh ? (
                <div mix={panelPad}>
                  <div mix={formCss}>
                    {opts.includeRun ? (
                      <Checkbox
                        label={`I have read the scrub report above for run ${opts.includeRun} and I understand its payloads and raw output can contain source code.`}
                        checked={ack}
                        onChange={(c) => ((ack = c), void handle.update())}
                      />
                    ) : null}
                    <Field label="Save to (optional full path; empty uses the support folder in the Drydock data folder)">
                      {(id) => <input id={id} value={dest} spellcheck={false} mix={[mono, on('input', (e) => ((dest = e.currentTarget.value), void handle.update()))]} />}
                    </Field>
                    <div mix={formActions}>
                      <Button variant="primary" icon={FileArchive} disabled={busy || (!!opts.includeRun && !ack)} mix={on('click', () => void runCreate())}>
                        {act.busy === 'create' ? 'Creating...' : 'Create the bundle'}
                      </Button>
                    </div>
                  </div>
                </div>
              ) : null}
              {created ? (
                <Done testId="bundle-created">
                  <span>
                    Created <code data-testid="bundle-path">{created.path}</code> ({formatBytes(created.size)}, {(created.files ?? []).length} file(s)). It has not been
                    uploaded anywhere.
                  </span>
                </Done>
              ) : null}
            </Group>
          ) : null}
        </div>
      </Page>
    );
  };
}

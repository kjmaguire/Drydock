// Export and import: export a project to a local zip (preview first: the files, the secret-scan result and what is never
// included; raw run output is opt-in behind an acknowledgement), and import an archive as a NEW project where everything is
// untrusted until the person decides. Works in every license state and with no project selected.

import { css, on, type Handle } from 'remix/component';
import { ArrowDownUp, FileDown, FileUp, ScanSearch } from 'lucide';
import type { ExportManifest, ExportPreview, ExportResult, ImportPreview, ImportResult, ImportStats } from '../../api/pkgTypes.ts';
import { formatBytes, formatDateTime } from '../../lib/format.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions } from '../../ui/form.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { Done, Group, intro, panelPad, plainList, settingsColumn, subhead, tableWrap, Warn } from '../settings/settings-kit.tsx';
import { exportKey, exportProjectId, pendingRows } from './data.ts';
import { RuleCounts } from './rule-counts.tsx';

const formCss = css({ display: 'grid', gap: t.s4 });
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
const statLine = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', fontSize: t.textMd });
const pendingCss = css({
  display: 'grid',
  gap: t.s2,
  '& > ul': { margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: '0', border: `1px solid ${t.border}`, borderRadius: t.rMd, overflow: 'hidden' },
  '& > ul > li': { padding: `6px ${t.s3}`, borderTop: `1px solid ${t.border}`, fontSize: t.textMd },
  '& > ul > li:first-child': { borderTop: '0' },
  '& > ul > li ul': { margin: '4px 0 0', paddingLeft: '18px' },
  '& > p': { margin: '0', fontSize: t.textSm, color: t.textMuted },
});

function ScanSummary(handle: Handle<{ m: ExportManifest }>) {
  return () => {
    const m = handle.props.m;
    const sc = m.scan;
    return (
      <div data-testid="export-scan" style={{ display: 'grid', gap: 'var(--space-2)' }}>
        <h3 mix={subhead}>Secret scan</h3>
        <p style={{ margin: '0' }}>
          Project data: <strong>{sc.dbRedactions}</strong> secret-like value(s) redacted.
          {m.includesRuns ? (
            <>
              {' '}
              Raw run output: {sc.runsFilesScanned} file(s) scanned, <strong>{sc.runsRedactions}</strong> redacted.
            </>
          ) : (
            ' Raw run output is not included.'
          )}
        </p>
        <RuleCounts by={sc.dbByRule} />
        {m.includesRuns ? <RuleCounts by={sc.runsByRule} /> : null}
        {(sc.runsExcluded ?? []).length > 0 ? (
          <>
            <p style={{ margin: '0' }}>Left out of the archive:</p>
            <ul mix={plainList}>
              {(sc.runsExcluded ?? []).map((x) => (
                <li key={x.path}>
                  <code>{x.path}</code>: {x.reason}
                </li>
              ))}
            </ul>
          </>
        ) : null}
        <p mix={faint} style={{ margin: '0' }}>
          Scanning is not a guarantee. Review what an archive contains before sharing it.
        </p>
      </div>
    );
  };
}

/** Everything an import leaves untrusted, with what each count means. */
export function PendingList(handle: Handle<{ p: ImportStats }>) {
  return () => {
    const p = handle.props.p;
    const providers = p.providersNeedingKeys ?? [];
    return (
      <div mix={pendingCss} data-testid="import-untrusted">
        <ul>
          {pendingRows(p).map(([label, n, note]) => (
            <li key={label} data-testid="import-pending-row">
              {label}: <strong>{n}</strong> <span mix={muted}>({note})</span>
            </li>
          ))}
          <li data-testid="import-pending-row">
            Providers that come without keys: <strong>{providers.length}</strong>{' '}
            <span mix={muted}>(imported disabled and unauthenticated; add the key yourself)</span>
            {providers.length > 0 ? (
              <ul>
                {providers.map((x) => (
                  <li key={x.id}>
                    {x.name} <code>{x.baseUrl}</code>
                  </li>
                ))}
              </ul>
            ) : null}
          </li>
        </ul>
        <p>Automatic scheduling and the end-to-end and preview commands are switched off in the imported project.</p>
      </div>
    );
  };
}

function ExportSection(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let picked = '';
  let includeRuns = false;
  let ack = false;
  let dest = '';
  let preview: (ExportPreview & { key: string }) | undefined;
  let result: ExportResult | undefined;

  const reset = () => {
    preview = undefined;
    result = undefined;
    ack = false;
    void handle.update();
  };

  return () => {
    // The person's pick, if it still exists; otherwise the selected project, otherwise the first one (so a project that appears
    // after the page opened, for example from an import on this page, is selectable straight away).
    const projects = s.workspace.projectList;
    const projectId = exportProjectId(projects, picked, s.workspace.project);
    const key = exportKey(projectId, includeRuns);
    const fresh = preview?.key === key;
    const m = preview?.manifest;
    const busy = act.busy !== '';

    const runPreview = async () => {
      result = undefined;
      ack = false;
      const r = await act.run('preview', () => s.client.projectExportPreview(projectId, includeRuns));
      if (r) preview = { ...r, key };
      void handle.update();
    };
    const runExport = async () => {
      const r = await act.run('export', () =>
        s.client.projectExport(projectId, { dest: dest.trim() || undefined, includeRuns, acknowledgeRuns: includeRuns && ack }),
      );
      if (r) result = r;
      void handle.update();
    };

    return (
      <Group
        title="Export a project"
        icon={FileDown}
        label="Export a project"
        description={
          <>An export is a zip of the project&apos;s data (tasks, rooms, memory, skills, requirements, settings and so on) from an allow-list. Preview shows exactly what would be in it.</>
        }
      >
        <div mix={panelPad}>
          <div mix={formCss}>
            <Field label="Project">
              {(id) => (
                <select
                  id={id}
                  aria-label="Project to export"
                  value={projectId}
                  mix={on('change', (e) => {
                    picked = e.currentTarget.value;
                    reset();
                  })}
                >
                  {projects.length === 0 ? <option value="">No projects yet</option> : null}
                  {projects.map((p) => (
                    <option key={p.id} value={p.id} selected={p.id === projectId}>
                      {p.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Checkbox
              label="Include raw run output (off by default)"
              description="Raw output can contain source code and anything an agent printed."
              checked={includeRuns}
              onChange={(c) => {
                includeRuns = c;
                reset();
              }}
            />
            <div mix={formActions}>
              <Button icon={ScanSearch} disabled={!projectId || busy} mix={on('click', () => void runPreview())}>
                {act.busy === 'preview' ? 'Scanning...' : 'Preview the export'}
              </Button>
            </div>
          </div>
        </div>
        <ErrorNote error={act.error} what="Export" />
        {preview && m ? (
          <div mix={previewCss} data-testid="export-preview" data-fresh={String(fresh)}>
            {!fresh ? <Warn>This preview is for different choices. Preview again before exporting.</Warn> : null}
            <p mix={statLine}>
              <strong>{m.projectName}</strong>
              <span>
                : {(m.files ?? []).length} file(s), {formatBytes(preview.totalBytes)}, made by Drydock {m.appVersion} (schema {m.schemaVersion}).
              </span>
            </p>
            {preview.warning ? <Warn testId="export-runs-warning">{preview.warning}</Warn> : null}
            <ScanSummary m={m} />
            <h3 mix={subhead}>Files in the archive</h3>
            <div mix={tableWrap}>
              <table aria-label="Files in the export">
                <thead>
                  <tr>
                    <th scope="col">Path</th>
                    <th scope="col">Size</th>
                  </tr>
                </thead>
                <tbody>
                  {(m.files ?? []).map((f) => (
                    <tr key={f.path}>
                      <td>
                        <code>{f.path}</code>
                      </td>
                      <td style={{ whiteSpace: 'nowrap' }}>{formatBytes(f.size)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <h3 mix={subhead}>Never included</h3>
            <ul mix={plainList} data-testid="export-never">
              {(m.neverIncluded ?? []).map((x) => (
                <li key={x}>{x}</li>
              ))}
            </ul>
          </div>
        ) : null}
        {preview && m && fresh ? (
          <div mix={panelPad}>
            <div mix={formCss}>
              {includeRuns ? (
                <Checkbox
                  label="I have read the secret-scan result above and I understand raw run output can contain source code and anything an agent printed."
                  checked={ack}
                  onChange={(c) => ((ack = c), void handle.update())}
                />
              ) : null}
              <Field label="Save to (optional full path; empty uses the exports folder in the Drydock data folder)">
                {(id) => <input id={id} mix={[mono, on('input', (e) => ((dest = e.currentTarget.value), void handle.update()))]} value={dest} spellcheck={false} />}
              </Field>
              <div mix={formActions}>
                <Button variant="primary" icon={FileDown} disabled={busy || (includeRuns && !ack)} mix={on('click', () => void runExport())}>
                  {act.busy === 'export' ? 'Exporting...' : 'Export'}
                </Button>
              </div>
            </div>
          </div>
        ) : null}
        {result ? (
          <Done testId="export-result">
            <span>
              Exported {formatBytes(result.size)} to <code data-testid="export-path">{result.path}</code>. Nothing was uploaded.
            </span>
          </Done>
        ) : null}
      </Group>
    );
  };
}

function ImportSection(handle: Handle) {
  const s = services(handle);
  const act = action(handle);
  let path = '';
  let repo = '';
  let name = '';
  let preview: (ImportPreview & { path: string }) | undefined;
  let result: ImportResult | undefined;

  const runPreview = async () => {
    result = undefined;
    const p = path.trim();
    const r = await act.run('importPreview', () => s.client.projectImportPreview(p));
    if (r) preview = { ...r, path: p };
    void handle.update();
  };
  const runImport = async () => {
    const r = await act.run('import', () => s.client.projectImport({ path: path.trim(), repoPath: repo.trim(), name: name.trim() || undefined }));
    if (r) {
      // Done: drop the preview so the same archive cannot be imported twice by another click.
      result = r;
      preview = undefined;
    }
    void handle.update();
  };

  return () => {
    const fresh = preview?.path === path.trim();
    const busy = act.busy !== '';
    return (
      <Group
        title="Import a project"
        icon={FileUp}
        label="Import a project"
        description={
          <>
            Importing creates a <strong>new</strong> project linked to a local Git repository you choose (an archive never contains your repository). Everything that comes in
            is untrusted until you decide. Importing starts nothing.
          </>
        }
      >
        <ErrorNote error={act.error} what="Import" />
        <div mix={panelPad}>
          <div mix={formCss}>
            <Field label="Archive path (a .drydock-project.zip on this computer)">
              {(id) => (
                <input
                  id={id}
                  value={path}
                  spellcheck={false}
                  placeholder="C:\Users\you\project.drydock-project.zip"
                  mix={[
                    mono,
                    on('input', (e) => {
                      path = e.currentTarget.value;
                      preview = undefined;
                      result = undefined;
                      void handle.update();
                    }),
                  ]}
                />
              )}
            </Field>
            <div mix={formActions}>
              <Button icon={ScanSearch} disabled={!path.trim() || busy} mix={on('click', () => void runPreview())}>
                Preview the import
              </Button>
            </div>
          </div>
        </div>
        {preview && fresh ? (
          <div mix={previewCss} data-testid="import-preview">
            <p mix={statLine}>
              <span>
                <strong>{preview.manifest.projectName}</strong>, exported by Drydock {preview.manifest.appVersion} on {formatDateTime(preview.manifest.created)} (schema{' '}
                {preview.manifest.schemaVersion}; this Drydock uses {preview.appSchema}).
              </span>
              {preview.needsMigration ? <Badge tone="info">will be migrated forward</Badge> : null}
            </p>
            <h3 mix={subhead}>What is in it</h3>
            <ul mix={plainList}>
              {Object.entries(preview.counts ?? {}).map(([k, n]) => (
                <li key={k}>
                  {k}: {n}
                </li>
              ))}
            </ul>
            <h3 mix={subhead}>What will not be trusted</h3>
            <PendingList p={preview.pending} />
            <div mix={formCss}>
              <Field label="Local repository to link (path)">
                {(id) => (
                  <input
                    id={id}
                    value={repo}
                    spellcheck={false}
                    placeholder="C:\src\my-repo"
                    mix={[mono, on('input', (e) => ((repo = e.currentTarget.value), void handle.update()))]}
                  />
                )}
              </Field>
              <Field label="New project name (optional)">
                {(id) => <input id={id} value={name} mix={on('input', (e) => ((name = e.currentTarget.value), void handle.update()))} />}
              </Field>
              <div mix={formActions}>
                <Button variant="primary" icon={FileUp} disabled={!repo.trim() || busy} mix={on('click', () => void runImport())}>
                  {act.busy === 'import' ? 'Importing...' : 'Import as a new project'}
                </Button>
              </div>
            </div>
          </div>
        ) : null}
        {result ? (
          <div mix={previewCss} role="status" data-testid="import-result" data-project-id={result.projectId}>
            <p>
              Imported <strong data-testid="import-result-name">{result.name}</strong> as a new project ({result.idsRemapped} ids remapped, {result.runFilesRestored} run
              file(s) restored
              {result.migratedForward ? ', migrated forward' : ''}). Choose it in the projects rail on the left.
            </p>
            <p>It came in untrusted:</p>
            <PendingList p={result.pending} />
          </div>
        ) : null}
      </Group>
    );
  };
}

export function DataScreen(_handle: Handle) {
  return () => (
    <Page title="Export and import" icon={ArrowDownUp} subtitle="Local files only">
      <div mix={settingsColumn}>
        <p mix={intro}>Export and import work in every license state. Files are written to and read from this computer only.</p>
        <ExportSection />
        <ImportSection />
      </div>
    </Page>
  );
}

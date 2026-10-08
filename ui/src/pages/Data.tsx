import { useRef, useState } from 'react';
import type { ExportManifest, ExportPreview, ExportResult, ImportPreview, ImportResult, ImportStats } from '../api/pkgTypes';
import type { Project } from '../api/types';
import { formatBytes, formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { Badge, ErrorNote, Page } from '../components/common';

export function RuleCounts({ by }: { by: Record<string, number> | undefined }) {
  const entries = Object.entries(by ?? {});
  if (entries.length === 0) return null;
  return (
    <ul className="rule-counts">
      {entries.map(([k, n]) => (
        <li key={k}>
          <code>{k}</code>: {n}
        </li>
      ))}
    </ul>
  );
}

function ScanSummary({ m }: { m: ExportManifest }) {
  const sc = m.scan;
  return (
    <div data-testid="export-scan">
      <h3>Secret scan</h3>
      <p>
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
          <p>Left out of the archive:</p>
          <ul>
            {(sc.runsExcluded ?? []).map((x) => (
              <li key={x.path}>
                <code>{x.path}</code>: {x.reason}
              </li>
            ))}
          </ul>
        </>
      ) : null}
      <p className="muted">Scanning is not a guarantee. Review what an archive contains before sharing it.</p>
    </div>
  );
}

function ExportSection({ projects, current }: { projects: Project[]; current: Project | undefined }) {
  const client = useClient();
  const act = useAction();
  // The user's pick, if it still exists; otherwise the sidebar's project, otherwise the first one (so a project that appears
  // after mount, for example from an import on this page, is selectable straight away).
  const [picked, setPicked] = useState('');
  const projectId = projects.some((p) => p.id === picked) ? picked : (current?.id ?? projects[0]?.id ?? '');
  const [includeRuns, setIncludeRuns] = useState(false);
  const [ack, setAck] = useState(false);
  const [dest, setDest] = useState('');
  const [preview, setPreview] = useState<(ExportPreview & { key: string }) | undefined>();
  const [result, setResult] = useState<ExportResult | undefined>();
  const key = `${projectId}|${includeRuns}`;
  const fresh = preview?.key === key;
  const m = preview?.manifest;

  const reset = () => {
    setPreview(undefined);
    setResult(undefined);
    setAck(false);
  };
  return (
    <section aria-label="Export a project">
      <h2>Export a project</h2>
      <p className="muted">
        An export is a zip of the project&apos;s data (tasks, rooms, memory, skills, requirements, settings and so on) from an allow-list. Preview shows exactly what would be in it.
      </p>
      <div className="form">
        <label>
          Project
          <select aria-label="Project to export" value={projectId} onChange={(e) => { setPicked(e.target.value); reset(); }}>
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <label className="inline">
          <input type="checkbox" checked={includeRuns} onChange={(e) => { setIncludeRuns(e.target.checked); reset(); }} /> Include raw run output (off by default)
        </label>
        <div className="form-actions">
          <button
            disabled={!projectId || act.busy !== ''}
            onClick={async () => {
              setResult(undefined);
              setAck(false);
              const r = await act.run('preview', () => client.projectExportPreview(projectId, includeRuns));
              if (r) setPreview({ ...r, key });
            }}
          >
            {act.busy === 'preview' ? 'Scanning...' : 'Preview the export'}
          </button>
        </div>
      </div>
      <ErrorNote error={act.error} what="Export" />
      {preview && m ? (
        <div data-testid="export-preview" data-fresh={String(fresh)}>
          {!fresh ? <p className="warn-note">This preview is for different choices. Preview again before exporting.</p> : null}
          <p>
            <strong>{m.projectName}</strong>: {(m.files ?? []).length} file(s), {formatBytes(preview.totalBytes)}, made by Drydock {m.appVersion} (schema {m.schemaVersion}).
          </p>
          {preview.warning ? (
            <p className="warn-note" role="note" data-testid="export-runs-warning">
              {preview.warning}
            </p>
          ) : null}
          <ScanSummary m={m} />
          <h3>Files in the archive</h3>
          <table className="table" aria-label="Files in the export">
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
                  <td>{formatBytes(f.size)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <h3>Never included</h3>
          <ul data-testid="export-never">
            {(m.neverIncluded ?? []).map((x) => (
              <li key={x}>{x}</li>
            ))}
          </ul>
        </div>
      ) : null}
      {preview && m && fresh ? (
        <div className="form">
          {includeRuns ? (
            <label className="inline">
              <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} /> I have read the secret-scan result above and I understand raw run output can contain
              source code and anything an agent printed.
            </label>
          ) : null}
          <label>
            Save to (optional full path; empty uses the exports folder in the Drydock data folder)
            <input value={dest} onChange={(e) => setDest(e.target.value)} spellCheck={false} />
          </label>
          <div className="form-actions">
            <button
              className="primary"
              disabled={act.busy !== '' || (includeRuns && !ack)}
              onClick={async () => {
                const r = await act.run('export', () => client.projectExport(projectId, { dest: dest.trim() || undefined, includeRuns, acknowledgeRuns: includeRuns && ack }));
                if (r) setResult(r);
              }}
            >
              {act.busy === 'export' ? 'Exporting...' : 'Export'}
            </button>
          </div>
        </div>
      ) : null}
      {result ? (
        <p role="status" data-testid="export-result">
          Exported {formatBytes(result.size)} to <code data-testid="export-path">{result.path}</code>. Nothing was uploaded.
        </p>
      ) : null}
    </section>
  );
}

export function PendingList({ p }: { p: ImportStats }) {
  const rows: [string, number, string][] = [
    ['Memory entries proposed', p.memoryPending, 'agents do not see them until you accept each one'],
    ['Skills offered', p.skillsPending, 'none is accepted or run until you accept it'],
    ['Requirement chunks quarantined', p.requirementChunksQuarantined, 'held until you release them'],
    ['Routing rules disabled', p.routingRulesDisabled, 'turn them on yourself if you want them'],
    ['Runs not resumed (cancelled)', p.runsNotResumed, 'work that was in progress is not restarted'],
    ['Tasks blocked', p.tasksBlocked, 'blocked until you decide'],
    ['Approvals cancelled', p.approvalsCancelled, 'open approvals from the source are not carried over'],
  ];
  const providers = p.providersNeedingKeys ?? [];
  return (
    <div data-testid="import-untrusted">
      <ul>
        {rows.map(([label, n, note]) => (
          <li key={label} data-testid="import-pending-row">
            {label}: <strong>{n}</strong> <span className="muted">({note})</span>
          </li>
        ))}
        <li data-testid="import-pending-row">
          Providers that come without keys: <strong>{providers.length}</strong> <span className="muted">(imported disabled and unauthenticated; add the key yourself)</span>
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
}

function ImportSection() {
  const client = useClient();
  const act = useAction();
  const [path, setPath] = useState('');
  const [repo, setRepo] = useState('');
  const [name, setName] = useState('');
  const [preview, setPreview] = useState<(ImportPreview & { path: string }) | undefined>();
  const [result, setResult] = useState<ImportResult | undefined>();
  const resultRef = useRef<HTMLDivElement>(null);
  const fresh = preview?.path === path.trim();
  return (
    <section aria-label="Import a project">
      <h2>Import a project</h2>
      <p className="muted">
        Importing creates a <strong>new</strong> project linked to a local Git repository you choose (an archive never contains your repository). Everything that comes in is
        untrusted until you decide. Importing starts nothing.
      </p>
      <ErrorNote error={act.error} what="Import" />
      <div className="form">
        <label>
          Archive path (a .drydock-project.zip on this computer)
          <input value={path} onChange={(e) => { setPath(e.target.value); setPreview(undefined); setResult(undefined); }} spellCheck={false} placeholder="C:\\Users\\you\\project.drydock-project.zip" />
        </label>
        <div className="form-actions">
          <button
            disabled={!path.trim() || act.busy !== ''}
            onClick={async () => {
              setResult(undefined);
              const r = await act.run('importPreview', () => client.projectImportPreview(path.trim()));
              if (r) setPreview({ ...r, path: path.trim() });
            }}
          >
            Preview the import
          </button>
        </div>
      </div>
      {preview && fresh ? (
        <div data-testid="import-preview">
          <p>
            <strong>{preview.manifest.projectName}</strong>, exported by Drydock {preview.manifest.appVersion} on {formatDateTime(preview.manifest.created)} (schema {preview.manifest.schemaVersion}; this Drydock uses {preview.appSchema}).{' '}
            {preview.needsMigration ? <Badge tone="info">will be migrated forward</Badge> : null}
          </p>
          <h3>What is in it</h3>
          <ul>
            {Object.entries(preview.counts ?? {}).map(([k, n]) => (
              <li key={k}>
                {k}: {n}
              </li>
            ))}
          </ul>
          <h3>What will not be trusted</h3>
          <PendingList p={preview.pending} />
          <div className="form">
            <label>
              Local repository to link (path)
              <input value={repo} onChange={(e) => setRepo(e.target.value)} spellCheck={false} placeholder="C:\\src\\my-repo" />
            </label>
            <label>
              New project name (optional)
              <input value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            <div className="form-actions">
              <button
                className="primary"
                disabled={!repo.trim() || act.busy !== ''}
                onClick={async () => {
                  const r = await act.run('import', () => client.projectImport({ path: path.trim(), repoPath: repo.trim(), name: name.trim() || undefined }));
                  if (r) {
                    // Done: drop the preview so the same archive cannot be imported twice by another click.
                    setResult(r);
                    setPreview(undefined);
                  }
                }}
              >
                {act.busy === 'import' ? 'Importing...' : 'Import as a new project'}
              </button>
            </div>
          </div>
        </div>
      ) : null}
      {result ? (
        <div role="status" ref={resultRef} data-testid="import-result" data-project-id={result.projectId}>
          <p>
            Imported <strong data-testid="import-result-name">{result.name}</strong> as a new project ({result.idsRemapped} ids remapped, {result.runFilesRestored} run file(s) restored
            {result.migratedForward ? ', migrated forward' : ''}). Choose it in the project switcher at the top of the sidebar.
          </p>
          <p>It came in untrusted:</p>
          <PendingList p={result.pending} />
        </div>
      ) : null}
    </section>
  );
}

export default function DataPage({ projects, current }: { projects: Project[]; current: Project | undefined }) {
  return (
    <Page title="Export and import">
      <p className="muted">Export and import work in every license state. Files are written to and read from this computer only.</p>
      <ExportSection projects={projects} current={current} />
      <ImportSection />
    </Page>
  );
}

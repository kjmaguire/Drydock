// Security scan of the workspace (/security-scan, the sidebar's Security; decisions P61, P97): scan any folder on this computer,
// a repository just downloaded or imported from elsewhere, before it becomes a project, or one of the projects. The same
// screen as the project's Security page (ScanPanel); the target field is the folder's path (typed, or chosen with Choose
// folder…) or a project picked beside it. The recent scans are every folder's and every project's. The scan only reads files
// and never runs the repository's code.

import { css, on, type Handle } from 'remix/component';
import type { ScanSummary } from '../../api/securityTypes.ts';
import { projectHasRepo, type DrydockEvent } from '../../api/types.ts';
import { services, watch } from '../../state/services.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { ChooseFolder } from '../shared/choose-folder.tsx';
import { ScanPanel, TargetBox, type ScanHistory } from './scan-panel.tsx';
import { folderScope, projectScope } from './scan-scope.ts';
import { folderName } from './security.ts';

type Mode = 'folder' | 'project';

// "Folder | Project": two radios drawn as one segmented control
const modesCss = css({
  display: 'inline-flex',
  flex: 'none',
  padding: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  '& label': {
    position: 'relative',
    display: 'inline-flex',
    alignItems: 'center',
    height: '28px',
    padding: '0 10px',
    borderRadius: t.rSm,
    color: t.textMuted,
    fontSize: t.textMd,
    cursor: 'pointer',
  },
  '& label:has(input:checked)': { background: t.bgActive, color: t.text },
  '& label:has(input:focus-visible)': { boxShadow: t.focus },
  '& input': { position: 'absolute', inset: '0', margin: '0', opacity: '0', cursor: 'pointer' },
});

/** Running scans first, then the newest. */
const byRecent = (a: ScanSummary, b: ScanSummary) => (a.status === 'running' ? 0 : 1) - (b.status === 'running' ? 0 : 1) || b.startedAt - a.startedAt;

export function WorkspaceSecurityScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  let mode: Mode = 'folder';
  /** The path in the folder field: what Scan reads. */
  let typed = '';
  /** The folder whose scans are shown: the core's full path once one was scanned or picked from the history ('' before). */
  let folder = '';
  /** The project chosen ('' until one is chosen: then the open project, else the first with a repository). */
  let chosen = '';

  const showFolder = (f: string) => {
    folder = f;
    typed = f;
    void handle.update();
  };
  // a folder scan still running (started before this page was opened) is shown at once
  void s.client
    .securityScanFolders(5)
    .then((list) => {
      const run = list.find((h) => h.status === 'running' && h.folder);
      if (run && !folder && !handle.signal.aborted) showFolder(run.folder!);
    })
    .catch(() => {});

  const pickHistory = (h: ScanSummary) => {
    if (h.folder) {
      mode = 'folder';
      showFolder(h.folder);
    } else if (h.projectId) {
      mode = 'project';
      chosen = h.projectId;
      void handle.update();
    }
  };
  const setMode = (m: Mode) => {
    mode = m;
    void handle.update();
  };

  // the recent scans of every folder and every project with a repository, kept until the projects change
  let history: ScanHistory | undefined;
  const historyOf = (ids: string[]): ScanHistory => {
    const key = `workspace:${ids.join(',')}`;
    if (history?.key === key) return history;
    history = {
      key,
      // one read of every project's and folder's scans (security.scan.recent): asked project by project, a workspace of fifty
      // projects made fifty calls (review 2026-10-27); a deleted project's or one without a repository is left out
      load: async (limit) => (await s.client.securityScanRecent(limit)).filter((h) => !!h.folder || ids.includes(h.projectId ?? '')).sort(byRecent).slice(0, limit),
      when: (e: DrydockEvent) => e.type.startsWith('security.scan.') || e.type.startsWith('security.finding.'),
    };
    return history;
  };

  return () => {
    const projects = s.workspace.projectList;
    const scannable = projects.filter((p) => projectHasRepo(p));
    const fallback = scannable.find((p) => p.id === s.workspace.projectId) ?? scannable[0];
    const projectId = chosen || fallback?.id || '';
    const project = projects.find((p) => p.id === projectId);
    const scope =
      mode === 'project' && projectId
        ? projectScope(s.client, projectId)
        : folderScope(
            s.client,
            mode === 'folder' ? folder : '',
            () => typed,
            (r) => showFolder(r.dir),
          );
    const canScan = mode === 'folder' ? typed.trim() !== '' : projectHasRepo(project);
    const nameOf = (h: ScanSummary) => (h.folder ? folderName(h.folder) : (projects.find((p) => p.id === h.projectId)?.name ?? 'a project'));

    const target = (
      <>
        <div mix={modesCss} role="radiogroup" aria-label="Scan a folder or a project">
          <label>
            <input type="radio" name={`scan-target-${handle.id}`} data-testid="scan-target-folder" checked={mode === 'folder'} mix={on('change', () => setMode('folder'))} />
            Folder
          </label>
          <label>
            <input type="radio" name={`scan-target-${handle.id}`} data-testid="scan-target-project" checked={mode === 'project'} mix={on('change', () => setMode('project'))} />
            Project
          </label>
        </div>
        {mode === 'folder' ? (
          <>
            <TargetBox
              note="a folder, not a project"
              end={
                <ChooseFolder
                  title="Choose the folder to scan"
                  testId="scan-choose-folder"
                  compact
                  onPick={(path) => {
                    typed = path;
                    void handle.update();
                  }}
                />
              }
            >
              <input
                id={`scan-folder-${handle.id}`}
                aria-label="Folder to scan"
                data-testid="scan-folder"
                spellcheck={false}
                autocomplete="off"
                placeholder="C:\Users\you\Downloads\some-repo"
                title="The folder's full path. It does not need to be a project, and nothing is added to one."
                value={typed}
                mix={on('input', (e) => {
                  typed = e.currentTarget.value;
                  void handle.update();
                })}
              />
            </TargetBox>
          </>
        ) : (
          <>
            <TargetBox note={scannable.length === 0 ? 'no project has a repository yet' : "the project's repository"}>
              <span class="value" title={project?.repoPath || undefined} data-testid="scan-target-path">
                {project?.repoPath || 'Choose a project'}
              </span>
            </TargetBox>
            {projects.length > 0 ? (
              <Select
                label="Project"
                testId="scan-project"
                size="sm"
                value={projectId}
                options={projects.map((p) => ({ value: p.id, label: p.name, disabled: !projectHasRepo(p), hint: projectHasRepo(p) ? undefined : 'no repository yet' }))}
                onChange={(v) => {
                  chosen = v;
                  void handle.update();
                }}
                style={{ width: 'auto', maxWidth: '200px' }}
              />
            ) : null}
          </>
        )}
      </>
    );

    return (
      <ScanPanel
        scope={scope}
        canScan={canScan}
        title="Security scan"
        testId="workspace-security"
        target={target}
        nameOf={nameOf}
        history={historyOf(scannable.map((p) => p.id))}
        onPick={pickHistory}
      />
    );
  };
}

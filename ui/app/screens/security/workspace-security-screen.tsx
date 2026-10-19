// Security scan of the workspace (/security-scan, decision P61): scan any folder on this computer, a repository just
// downloaded or imported from elsewhere, before it becomes a project, or one of the projects. The same engines, progress,
// findings, tools and history as the project's Security page (ScanPanel); a folder scan belongs to no project, its history is
// the recent folder scans and its Opengrep rules folder is the workspace's. The scan only reads files and never runs the
// repository's code.

import { css, on, type Handle } from 'remix/component';
import { ShieldCheck } from 'lucide';
import type { ScanSummary } from '../../api/securityTypes.ts';
import { projectHasRepo } from '../../api/types.ts';
import { services, watch } from '../../state/services.tsx';
import { Notice } from '../../ui/feedback.tsx';
import { Page, card } from '../../ui/layout.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { ScanPanel } from './scan-panel.tsx';
import { folderScope, projectScope } from './scan-scope.ts';
import { ChooseFolder, folderRow } from '../shared/choose-folder.tsx';

type Mode = 'folder' | 'project';

const targetCss = css({
  display: 'grid',
  gap: t.s3,
  margin: '0',
  '& legend': { padding: '0', marginBottom: t.s2, fontSize: t.textMd, fontWeight: '600' },
  '& .modes': { display: 'flex', gap: t.s4, flexWrap: 'wrap' },
  '& .modes label': { display: 'inline-flex', alignItems: 'center', gap: '6px', fontSize: t.textMd },
  '& .field': { display: 'grid', gap: '6px' },
  '& .field label': { fontSize: t.textSm, fontWeight: '500' },
  '& .field input': { fontFamily: t.mono, fontSize: t.textSm, width: '100%', minWidth: '0' },
  '& .field select': { maxWidth: '100%' },
  '& .hint': { margin: '0', fontSize: t.textSm, color: t.textMuted },
});

export function WorkspaceSecurityScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  let mode: Mode = 'folder';
  /** The path in the folder field: what Scan reads. */
  let typed = '';
  /** The folder whose scans are shown: the core's full path once one was scanned or picked from the history ('' before). */
  let folder = '';
  /** The project chosen in the list ('' until one is chosen: then the open project, else the first with a repository). */
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
    if (h.folder) showFolder(h.folder);
  };
  const setMode = (m: Mode) => {
    mode = m;
    void handle.update();
  };

  return () => {
    const projects = s.workspace.projectList;
    const fallback = projects.find((p) => p.id === s.workspace.projectId && projectHasRepo(p)) ?? projects.find((p) => projectHasRepo(p));
    const projectId = chosen || fallback?.id || '';
    const project = projects.find((p) => p.id === projectId);
    const scope =
      mode === 'project'
        ? projectId
          ? projectScope(s.client, projectId)
          : undefined
        : folderScope(
            s.client,
            folder,
            () => typed,
            (r) => showFolder(r.dir),
          );
    const canScan = mode === 'folder' ? typed.trim() !== '' : projectHasRepo(project);

    return (
      <Page title="Security scan" icon={ShieldCheck} testId="workspace-security" subtitle="Any repository on this computer" guide="scan">
        <Notice tone="info" testId="scan-reads-only">
          The scan only reads files: it never runs the repository's code, its scripts or its package manager, so it is safe for a repository you just downloaded.
        </Notice>

        <form
          mix={[card, on('submit', (e) => e.preventDefault())]}
          aria-label="What to scan"
        >
          <fieldset mix={targetCss} style={{ border: '0', padding: '0' }}>
            <legend>What to scan</legend>
            <div class="modes" role="radiogroup" aria-label="Scan a folder or a project">
              <label>
                <input type="radio" name={`scan-target-${handle.id}`} data-testid="scan-target-folder" checked={mode === 'folder'} mix={on('change', () => setMode('folder'))} />
                A folder on this computer
              </label>
              <label>
                <input type="radio" name={`scan-target-${handle.id}`} data-testid="scan-target-project" checked={mode === 'project'} mix={on('change', () => setMode('project'))} />
                One of your projects
              </label>
            </div>
            {mode === 'folder' ? (
              <div class="field">
                <label for={`scan-folder-${handle.id}`}>Folder</label>
                <div mix={folderRow}>
                  <input
                    id={`scan-folder-${handle.id}`}
                    data-testid="scan-folder"
                    spellcheck={false}
                    autocomplete="off"
                    placeholder="The full path, for example C:\Users\you\Downloads\some-repo"
                    value={typed}
                    mix={on('input', (e) => {
                      typed = e.currentTarget.value;
                      void handle.update();
                    })}
                  />
                  <ChooseFolder
                    title="Choose the folder to scan"
                    testId="scan-choose-folder"
                    onPick={(path) => {
                      typed = path;
                      void handle.update();
                    }}
                  />
                </div>
                <p class="hint">
                  Choose the folder or paste its full path. It does not need to be a project, and nothing is added to one.
                </p>
              </div>
            ) : (
              <div class="field">
                <label for={`scan-project-${handle.id}`}>Project</label>
                {projects.length === 0 ? (
                  <p class="hint">You have no projects yet: scan a folder instead.</p>
                ) : (
                  <Select
                    id={`scan-project-${handle.id}`}
                    testId="scan-project"
                    value={projectId}
                    options={projects.map((p) => ({ value: p.id, label: p.name, disabled: !projectHasRepo(p), hint: projectHasRepo(p) ? undefined : 'no repository yet' }))}
                    onChange={(v) => {
                      chosen = v;
                      void handle.update();
                    }}
                  />
                )}
                <p class="hint">Scans the project's working tree, with the project's own rules folder and history, as its Security page does.</p>
              </div>
            )}
          </fieldset>
        </form>

        {scope ? (
          <ScanPanel
            scope={scope}
            canScan={canScan}
            historyTitle={mode === 'folder' ? 'Recent folder scans' : `History of ${project?.name ?? 'the project'}`}
            rulesHint={mode === 'folder' ? 'Folder scans use this rules folder; each project keeps its own on its Security page.' : undefined}
            onPick={mode === 'folder' ? pickHistory : undefined}
          />
        ) : null}
      </Page>
    );
  };
}

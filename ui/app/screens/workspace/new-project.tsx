// New project (decision P143): Home's "New project" button, Ctrl+K's New project and the old wizard's address (/project/new,
// which redirects to /home?new=project) open this dialog. A name, and where the code lives as one segmented choice: a New folder
// (in the usual code folder, named after the project, editable), Open a folder (FolderField: what is there, and "Create a Git
// repository here"), or Clone from GitHub (RepoPicker: the repository and the folder to clone into). Create makes the project with
// the start box's defaults (start.ts newProjectSetup: one project.setup, Solo's builder as the crew) and starts no work; the
// person lands on the project's Work. The name follows the folder or repository until the person types one.

import { css, navigate, on, type Handle } from 'remix/component';
import type { RepoFolder } from '../../api/folderClient.ts';
import type { GitHubRepo } from '../../api/types.ts';
import { projectWorkHref } from '../../lib/project-work.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import type { NewProjectWhere } from '../../shell/new.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Segmented } from '../../ui/segmented.tsx';
import { t } from '../../ui/tokens.ts';
import { FolderField } from '../project/folder-field.tsx';
import { RepoPicker } from '../project/repo-picker.tsx';
import { currentCheck, folderProblem } from '../project/wizard.ts';
import { defaultProjectName, newProjectFolder, newProjectSetup, type Where } from './start.ts';

const bodyCss = css({
  display: 'grid',
  gap: t.s4,
  minWidth: '0',
  '& .acts': { display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: t.s2 },
  '& .acts .progress': { marginRight: 'auto', color: t.textMuted, fontSize: t.textSm },
  '& input.path': { width: '100%', fontFamily: t.mono, fontSize: t.textSm },
  '& .where': { display: 'grid', gap: '6px', justifyItems: 'start' },
  '& .where .cap': { fontSize: t.textSm, fontWeight: '500' },
});

const WHERE: { value: NewProjectWhere; label: string; hint: string }[] = [
  { value: 'new', label: 'New folder', hint: 'An empty folder with a new Git repository' },
  { value: 'folder', label: 'Open a folder', hint: 'A folder already on this PC' },
  { value: 'clone', label: 'Clone from GitHub', hint: 'A copy of a GitHub repository, with your gh sign-in' },
];

export function NewProjectDialog(handle: Handle<{ open: boolean; where?: NewProjectWhere; onClose: () => void; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href).catch(() => {}));

  let kind: NewProjectWhere = handle.props.where ?? 'new';
  let wasOpen = false;
  let name = '';
  let named = false; // the person typed a name: it no longer follows the folder or repository
  let newPath: string | undefined; // the new folder as typed; undefined: it follows the name
  let folder = { path: '', check: undefined as RepoFolder | undefined, init: false };
  let clone = { repo: undefined as GitHubRepo | undefined, cloneDir: '' };
  let progress = '';

  const catalog = query(handle, { enabled: () => handle.props.open, load: () => s.client.agentCatalogList() });
  const adapters = query(handle, { enabled: () => handle.props.open, load: () => s.client.adapterList() });
  const baseDir = query(handle, { enabled: () => handle.props.open, load: () => s.client.githubCloneDir(true).catch(() => '') });

  const set = (f: () => void) => (f(), void handle.update());

  const where = (): Where => {
    if (kind === 'folder') return { kind: 'folder', path: folder.path.trim().replace(/^"+|"+$/g, ''), init: folder.init };
    if (kind === 'clone') return { kind: 'clone', repo: clone.repo?.fullName ?? '', cloneDir: clone.cloneDir.trim() };
    return { kind: 'new', path: newPath ?? newProjectFolder(baseDir.data ?? '', name) };
  };
  const shownName = () => (named || kind === 'new' ? name : defaultProjectName(where()) || name);

  const reset = () => {
    kind = handle.props.where ?? 'new';
    name = '';
    named = false;
    newPath = undefined;
    folder = { path: '', check: undefined, init: false };
    clone = { repo: undefined, cloneDir: '' };
    progress = '';
  };

  const create = async () => {
    if (act.busy) return;
    const w = where();
    const fp = kind === 'folder' ? folderProblem(currentCheck({ localPath: folder.path, localCheck: folder.check }), folder.init) : undefined;
    const req = newProjectSetup({ where: w, name: shownName(), catalog: catalog.data ?? [], fallbackAdapter: adapters.data?.find((a) => a.detection.installed)?.id ?? 'claude' });
    if (fp || 'problem' in req) return;
    progress = kind === 'clone' ? 'Cloning…' : 'Creating project…';
    // a first run shows Home in place of the project's pages: the new project must not replace it before the page moves on
    s.workspace.setSetupOpen(true);
    const r = await act.run('create', () => s.client.projectSetup(req));
    progress = '';
    s.workspace.setSetupOpen(false);
    if (!r) return void handle.update();
    if (r.warnings.length) s.ui.flash(`${r.project.name}: ${r.warnings.join(' ')}`, 'warn');
    s.workspace.selectProject(r.project.id);
    s.workspace.projects.reload();
    reset();
    handle.props.onClose();
    go(projectWorkHref());
  };

  return () => {
    const { open, onClose } = handle.props;
    if (open && !wasOpen) reset();
    wasOpen = open;
    const busy = !!act.busy;
    const readOnly = s.workspace.license.readOnly;
    const w = where();
    const n = shownName();
    const fp = kind === 'folder' ? folderProblem(currentCheck({ localPath: folder.path, localCheck: folder.check }), folder.init) : undefined;
    const plan = newProjectSetup({ where: w, name: n, catalog: catalog.data ?? [], fallbackAdapter: 'claude' });
    const problem = fp ?? ('problem' in plan ? plan.problem : undefined);
    return (
      <Dialog open={open} title="New project" wide={kind === 'clone'} onClose={() => (busy ? undefined : onClose())} testId="new-project-dialog">
        {open ? (
          <form
            mix={[
              bodyCss,
              on('submit', (e) => {
                e.preventDefault();
                void create();
              }),
            ]}
            aria-label="New project"
          >
            <ReadOnlyNote />
            <Field label="Project name">
              {(id) => (
                <input
                  id={id}
                  value={n}
                  placeholder={kind === 'new' ? 'my-app' : 'Named after the folder'}
                  autocomplete="off"
                  spellcheck={false}
                  data-autofocus
                  readOnly={busy}
                  data-testid="new-project-name"
                  mix={on('input', (e) => set(() => ((name = e.currentTarget.value), (named = true))))}
                />
              )}
            </Field>
            <div class="where">
              <span class="cap" aria-hidden="true">
                Where its code lives
              </span>
              <Segmented label="Where its code lives" value={kind} options={WHERE} disabled={busy} onChange={(v) => set(() => (kind = v as NewProjectWhere))} testId="new-project-where" />
            </div>
            {kind === 'new' ? (
              <Field label="New folder" hint="Drydock makes the folder and a Git repository in it (branch main, a first commit).">
                {(id) => (
                  <input
                    id={id}
                    class="path"
                    value={w.kind === 'new' ? w.path : ''}
                    placeholder="C:\Users\you\source\repos\my-app"
                    spellcheck={false}
                    autocomplete="off"
                    readOnly={busy}
                    data-testid="new-project-folder"
                    mix={on('input', (e) => set(() => (newPath = e.currentTarget.value)))}
                  />
                )}
              </Field>
            ) : kind === 'folder' ? (
              <FolderField
                label="Folder"
                value={folder.path}
                onChange={(v) => set(() => (folder = { ...folder, path: v }))}
                check={folder.check}
                onCheck={(c) => set(() => (folder = { ...folder, check: c }))}
                initRepo={folder.init}
                onInitRepo={(v) => set(() => (folder = { ...folder, init: v }))}
              />
            ) : (
              <RepoPicker
                selected={clone.repo}
                cloneDir={clone.cloneDir}
                onSelect={(r) => set(() => (clone = { ...clone, repo: r }))}
                onCloneDir={(d) => set(() => (clone = { ...clone, cloneDir: d }))}
                unavailableHint="Choose New folder or Open a folder instead."
              />
            )}
            <ErrorNote error={act.error} what="Could not create the project" />
            <div class="acts">
              {progress ? (
                <span class="progress" role="status" data-testid="new-project-progress">
                  {progress}
                </span>
              ) : problem && (n.trim() || kind !== 'new') ? (
                <span class="progress" data-testid="new-project-problem">
                  {problem}
                </span>
              ) : null}
              <Button type="button" variant="secondary" disabled={busy} mix={on('click', onClose)}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" disabled={busy || !!problem || readOnly} title={readOnly ? s.workspace.license.reason : problem} data-testid="new-project-create">
                {busy ? 'Creating…' : 'Create project'}
              </Button>
            </div>
          </form>
        ) : null}
      </Dialog>
    );
  };
}

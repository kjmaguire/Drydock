// Project settings for a project made without a repository, and for every project's brief: attach a repository once (a
// local folder, or a GitHub repository cloned with the user's own gh), and edit the brief every agent reads.

import { css, on, type Handle } from 'remix/component';
import { FolderGit2, GitBranch } from 'lucide';
import { PROJECT_BRIEF_MAX, type GitHubRepo, type Project } from '../../api/types.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { RepoPicker } from './repo-picker.tsx';
import { SettingsGroup } from './settings-kit.tsx';
import { cloneTarget } from './wizard.ts';

const padCss = css({ display: 'grid', gap: t.s4, padding: t.s4, '& input, & textarea': { width: '100%' } });
const segCss = css({
  display: 'inline-flex',
  padding: '2px',
  gap: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  width: 'fit-content',
  '& label': { position: 'relative', display: 'inline-flex', alignItems: 'center', gap: '6px', height: '28px', padding: `0 ${t.s3}`, borderRadius: t.rSm, color: t.textMuted, fontWeight: '500', cursor: 'pointer' },
  '& label:has(input:checked)': { background: t.bgRaised, color: t.text, boxShadow: t.shadowSm },
  '& label:has(input:focus-visible)': { boxShadow: t.focus },
  // the radio covers its label, invisible: the whole segment is the control (and stays clickable for tests and AT)
  '& input': { position: 'absolute', inset: '0', width: '100%', height: '100%', margin: '0', opacity: '0', cursor: 'pointer' },
});
const endCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  '& .hint': { flex: '1', color: t.textMuted, fontSize: t.textSm, minWidth: '0' },
  '& [role="status"]': { color: t.success, fontSize: t.textSm, fontWeight: '500' },
});
const countCss = css({ fontVariantNumeric: 'tabular-nums', '&[data-over="true"]': { color: t.danger } });

/** Attach a repository to a project that has none (once: a repository is never swapped). */
export function AttachRepository(handle: Handle<{ project: Project }>) {
  const s = services(handle);
  const act = action(handle);
  let kind: 'local' | 'github' = 'local';
  let path = '';
  let repo: GitHubRepo | undefined;
  let cloneDir = '';

  const attach = async () => {
    const p = handle.props.project;
    const ok = await act.run('attach', async () => {
      const dir = kind === 'github' && repo ? (await s.client.githubClone(repo.fullName, cloneDir.trim())).path : path.trim();
      await s.client.projectAttachRepo(p.id, dir);
      return true;
    });
    if (ok) s.workspace.projects.reload();
  };

  return () => {
    const ready = kind === 'local' ? !!path.trim() : !!repo && !!cloneDir.trim();
    return (
      <SettingsGroup
        title="Attach a repository"
        label="Attach a repository"
        description="Until a repository is attached, agents can plan and talk but not change code. A repository is attached once; it cannot be swapped later."
        testId="attach-repo"
      >
        <form mix={[padCss, on('submit', (e) => (e.preventDefault(), void attach()))]}>
          <ErrorNote error={act.error} what="Repository not attached" />
          <div role="radiogroup" aria-label="Repository source" mix={segCss}>
            <label>
              <input type="radio" name={`att-${handle.id}`} checked={kind === 'local'} mix={on('change', () => ((kind = 'local'), void handle.update()))} />
              Local folder
            </label>
            <label>
              <input type="radio" name={`att-${handle.id}`} checked={kind === 'github'} mix={on('change', () => ((kind = 'github'), void handle.update()))} />
              GitHub
            </label>
          </div>
          {kind === 'local' ? (
            <Field label="Repository path" hint="A folder with a Git repository that already has a commit on a branch.">
              {(id) => <input id={id} value={path} placeholder="C:\src\my-repo" spellcheck={false} autocomplete="off" mix={on('input', (e) => ((path = e.currentTarget.value), void handle.update()))} />}
            </Field>
          ) : (
            <RepoPicker
              selected={repo}
              cloneDir={cloneDir}
              onSelect={(r) => ((repo = r), void handle.update())}
              onCloneDir={(d) => ((cloneDir = d), void handle.update())}
              unavailableHint="Attach a local folder instead."
            />
          )}
          <div mix={endCss}>
            <span class="hint">{kind === 'github' && repo ? `Clones into ${cloneTarget(cloneDir, repo)}, then attaches it.` : 'Drydock checks the folder before attaching it.'}</span>
            <Button type="submit" variant="primary" size="sm" icon={kind === 'github' ? GitBranch : FolderGit2} disabled={!!act.busy || !ready}>
              {act.busy ? (kind === 'github' ? 'Cloning...' : 'Attaching...') : kind === 'github' ? 'Clone and attach' : 'Attach repository'}
            </Button>
          </div>
        </form>
      </SettingsGroup>
    );
  };
}

/** The project brief: what the project is and how the team should work. Every agent of the project is given it. */
export function BriefPanel(handle: Handle<{ project: Project }>) {
  const s = services(handle);
  const act = action(handle);
  let text = handle.props.project.brief ?? '';
  let base = `${handle.props.project.id}:${handle.props.project.brief ?? ''}`;
  let saved = false;

  const save = async () => {
    saved = false;
    const ok = await act.run('save', async () => {
      await s.client.projectSetBrief(handle.props.project.id, text);
      return true;
    });
    if (!ok) return;
    saved = true;
    s.workspace.projects.reload();
    void handle.update();
  };

  return () => {
    const p = handle.props.project;
    const key = `${p.id}:${p.brief ?? ''}`;
    if (key !== base) {
      base = key;
      text = p.brief ?? '';
    }
    const n = text.trim().length;
    const dirty = text.trim() !== (p.brief ?? '').trim();
    return (
      <SettingsGroup title="Brief" label="Brief" description="What is this project? How should the team work? Every agent of the project reads this on every turn, so keep it short.">
        <form mix={[padCss, on('submit', (e) => (e.preventDefault(), void save()))]}>
          <ErrorNote error={act.error} what="Brief not saved" />
          <Field label="Project brief">
            {(id) => <textarea id={id} rows={6} value={text} mix={on('input', (e) => ((text = e.currentTarget.value), (saved = false), void handle.update()))} />}
          </Field>
          <div mix={endCss}>
            <span class="hint" mix={countCss} data-over={n > PROJECT_BRIEF_MAX ? 'true' : undefined} data-testid="brief-count">
              {n} / {PROJECT_BRIEF_MAX} characters
            </span>
            {saved ? <span role="status">Saved.</span> : null}
            <Button type="submit" variant="primary" size="sm" disabled={!!act.busy || !dirty || n > PROJECT_BRIEF_MAX}>
              Save brief
            </Button>
          </div>
        </form>
      </SettingsGroup>
    );
  };
}

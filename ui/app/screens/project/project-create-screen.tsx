// Create a project from a local Git repository. Shown by the shell when no project exists (first run) and at /project/new.

import { css, navigate, on, type Handle } from 'remix/component';
import { FolderGit2, FolderPlus, GitMerge, ShieldCheck, Users } from 'lucide';
import { routes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';

const wrapCss = css({ display: 'grid', gap: t.s6, width: '100%', maxWidth: '560px', margin: `${t.s6} auto 0` });
const heroCss = css({
  display: 'grid',
  justifyItems: 'start',
  gap: t.s3,
  '& h2': { fontSize: t.text2xl, fontWeight: '600', letterSpacing: '-0.01em', margin: '0' },
  '& p': { margin: '0', color: t.textMuted, fontSize: t.textMd, lineHeight: '1.6' },
});
const heroIcon = css({ display: 'grid', placeItems: 'center', width: '44px', height: '44px', borderRadius: t.rLg, background: t.accentSoft, color: t.accentText });
const formCss = css({
  display: 'grid',
  gap: t.s4,
  padding: t.s5,
  border: `1px solid ${t.border}`,
  borderRadius: t.rXl,
  background: t.bgSurface,
  boxShadow: t.shadowSm,
  '& input': { width: '100%' },
});
const actionsCss = css({ display: 'flex', justifyContent: 'flex-end', gap: t.s2, paddingTop: t.s1 });
const pointsCss = css({
  display: 'grid',
  gap: t.s3,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': { display: 'flex', alignItems: 'flex-start', gap: t.s3, color: t.textMuted, fontSize: t.textSm },
  '& li > span:first-child': { display: 'inline-flex', marginTop: '1px', color: t.textFaint },
  '& strong': { color: t.text, fontWeight: '500' },
});

const POINTS: [IconNode, string, string][] = [
  [Users, 'A team of real agents.', 'Each agent is a coding CLI on this machine with a role; a different agent reviews the work.'],
  [FolderGit2, 'Separate worktrees.', 'Each task works in its own worktree and branch.'],
  [GitMerge, 'You approve every merge.', 'Nothing lands on your branch until you say so.'],
  [ShieldCheck, 'Your code stays here.', 'Drydock never uploads source code.'],
];

/** `onNavigate` replaces the router's navigate (tests). */
export function ProjectCreateScreen(handle: Handle<{ first?: boolean; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let name = '';
  let repoPath = '';

  const submit = async () => {
    // the core's own message on failure, e.g. "... is not a usable Git repository"
    const p = await act.run('create', () => s.client.projectCreate(name.trim(), repoPath.trim()));
    if (!p) return;
    s.workspace.selectProject(p.id);
    s.workspace.projects.reload();
    if (handle.props.onNavigate) handle.props.onNavigate(routes.home.href());
    else void navigate(routes.home.href());
  };

  return () => {
    const first = !!handle.props.first;
    const ro = s.workspace.license;
    return (
      <Page title={first ? 'Welcome to Drydock' : 'New project'} icon={FolderPlus}>
        <div mix={wrapCss}>
          <div mix={heroCss}>
            <span mix={heroIcon}>
              <Icon icon={first ? Users : FolderGit2} size={22} />
            </span>
            <h2>{first ? 'Start with a repository' : 'Add a project'}</h2>
            {first ? (
              <p>
                Drydock runs a team of real coding agents on a local Git repository. Pick a repository that already has a commit on a branch. Drydock works in separate
                worktrees and merges only after you approve.
              </p>
            ) : (
              <p>A project is one local Git repository with its own team, channels and tasks.</p>
            )}
          </div>
          <form mix={[formCss, on('submit', (e) => (e.preventDefault(), void submit()))]} aria-label={first ? 'Create your first project' : 'New project'}>
            <ErrorNote error={act.error} what="Project not created" />
            <ReadOnlyNote />
            <Field label="Repository path" hint="A folder with a Git repository that already has a commit on a branch.">
              {(id) => (
                <input
                  id={id}
                  value={repoPath}
                  placeholder="C:\src\my-repo"
                  required
                  spellcheck={false}
                  autocomplete="off"
                  mix={on('input', (e) => ((repoPath = e.currentTarget.value), void handle.update()))}
                />
              )}
            </Field>
            <Field label="Project name (optional)">
              {(id) => (
                <input
                  id={id}
                  value={name}
                  placeholder="defaults to the folder name"
                  autocomplete="off"
                  mix={on('input', (e) => ((name = e.currentTarget.value), void handle.update()))}
                />
              )}
            </Field>
            <div mix={actionsCss}>
              <Button type="submit" variant="primary" icon={FolderPlus} disabled={!!act.busy || ro.readOnly || !repoPath.trim()} title={ro.readOnly ? ro.reason : undefined}>
                {act.busy ? 'Creating...' : 'Create project'}
              </Button>
            </div>
          </form>
          {first ? (
            <ul mix={pointsCss} aria-label="How Drydock works">
              {POINTS.map(([icon, head, text]) => (
                <li key={head}>
                  <span>
                    <Icon icon={icon} size={16} />
                  </span>
                  <span>
                    <strong>{head}</strong> {text}
                  </span>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </Page>
    );
  };
}

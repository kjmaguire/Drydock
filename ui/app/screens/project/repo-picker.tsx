// Pick a GitHub repository through the user's own `gh` (its sign-in stays with gh; Drydock stores nothing of it): the account and its organizations,
// a search box, the repositories as a list of radio buttons, and the folder to clone into: typed, or chosen with the window's
// folder dialog (Choose folder…), prefilled with the folder the person last cloned into (github.cloneDir, decision P143), and the
// full path the clone makes shown under it. A repository can also be typed (owner/name or its address, W9), so a list GitHub will
// not give (an organization that refuses the listing) is no dead end, and a list that failed has Try again. When gh is missing
// it links to how to install it; when gh is not signed in, Sign in runs gh auth login in a terminal here (F4) and the list
// follows. A repository that is already a project's origin says "Already a project: acme" with Open (B7), and the folder the
// clone would make is checked as it is typed (repo.check): a clone of the same repository already there (its origin is the
// one picked) offers "Use this folder"; another repository, or a folder with files in it, says the clone would be refused, before anything is started. Used by Home's
// New project dialog and the start box's Clone from GitHub, and by Project settings (attach a repository).

import { css, navigate, on, type Handle, type RemixNode } from 'remix/component';
import { Lock, Search } from 'lucide';
import type { RepoFolder } from '../../api/folderClient.ts';
import type { GitHubRepo } from '../../api/types.ts';
import { projectWorkHref } from '../../lib/project-work.ts';
import { query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { ChooseFolder, folderRow } from '../shared/choose-folder.tsx';
import { GitHubSignIn } from '../shared/github-sign-in.tsx';
import { cloneTarget, pushedAgo, typedRepo } from './wizard.ts';

const wrapCss = css({ display: 'grid', gap: t.s3, minWidth: '0' });
const barCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(140px, 200px) minmax(0, 1fr)',
  gap: t.s2,
  alignItems: 'end',
  '& input, & select': { width: '100%' },
});
const searchCss = css({ position: 'relative', '& svg': { position: 'absolute', left: '9px', top: '50%', transform: 'translateY(-50%)', color: t.textFaint }, '& input': { paddingLeft: '28px' } });
const listCss = css({
  display: 'grid',
  // each row as tall as its content: with the list's set height, auto rows shrink to their floor (app.css gives a label
  // around a radio 28px) and run into each other
  gridAutoRows: 'max-content',
  maxHeight: '300px',
  overflowY: 'auto',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
});
const itemCss = css({
  display: 'grid',
  gridTemplateColumns: 'auto minmax(0, 1fr)',
  columnGap: t.s3,
  padding: `${t.s2} ${t.s3}`,
  borderBottom: `1px solid ${t.border}`,
  cursor: 'pointer',
  '&:last-child': { borderBottom: '0' },
  '&:hover': { background: t.bgHover },
  '&:has(input:checked)': { background: t.accentSoft },
  '& input': { marginTop: '3px' },
  '& .top': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0', fontWeight: '500' },
  '& .top .n': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .desc': { color: t.textMuted, fontSize: t.textSm, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .when': { color: t.textFaint, fontSize: t.textXs },
});
const targetCss = css({ fontFamily: t.mono, fontSize: t.textSm, color: t.text, overflowWrap: 'anywhere' });

/** The folder's origin is the repository the person picked (owner/name, letter case ignored as GitHub does). */
const sameRepo = (origin: string | undefined, fullName: string | undefined) => !!origin && !!fullName && origin.toLowerCase() === fullName.toLowerCase();

export function RepoPicker(
  handle: Handle<{
    selected: GitHubRepo | undefined;
    cloneDir: string;
    onSelect: (r: GitHubRepo | undefined) => void;
    onCloneDir: (dir: string) => void;
    unavailableHint?: RemixNode;
    /** The repository is already cloned at the target: use that folder instead (B7). Without it the picker only says so. */
    onUseFolder?: (path: string) => void;
    /** Open the project that already has this repository; without it the picker selects it and goes to its Work. */
    onOpenProject?: (projectId: string) => void;
  }>,
) {
  const s = services(handle);
  let owner = '';
  let text = '';
  let searched = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  let askedDir = false;
  let typed = ''; // a repository typed as owner/name or its address
  let fromTyped = false; // the selection is the typed repository (not a row of the list)

  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  const listId = `repos-${handle.id}`;

  const status = query(handle, { load: () => s.client.githubStatus(), when: (e) => e.type === 'github.sign_in_finished' });
  // what is at the folder the clone would make, asked a moment after the folder or the repository stops changing
  let target: RepoFolder | undefined;
  let asked = '';
  let checkTimer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(checkTimer));
  const checkTarget = (path: string) => {
    if (path === asked) return;
    asked = path;
    clearTimeout(checkTimer);
    target = undefined;
    if (!path) return;
    checkTimer = setTimeout(() => {
      void Promise.resolve()
        .then(() => s.client.repoCheck(path))
        .then(
          (c) => {
            if (handle.signal.aborted || asked !== path) return;
            target = c;
            void handle.update();
          },
          () => undefined,
        );
    }, 300);
  };
  const openProject = (id: string) => {
    if (handle.props.onOpenProject) return handle.props.onOpenProject(id);
    s.workspace.selectProject(id);
    void navigate(projectWorkHref()).catch(() => {});
  };
  const signedIn = () => status.data?.loggedIn === true;
  const owners = query(handle, { load: () => s.client.githubOwners(), enabled: signedIn, key: () => signedIn() });
  const repos = query(handle, {
    load: () => s.client.githubRepos(owner || owners.data?.[0] || '', searched),
    enabled: () => signedIn() && !!owners.data,
    key: () => [owner || owners.data?.[0] || '', searched],
  });

  const onText = (v: string) => {
    text = v;
    clearTimeout(timer);
    timer = setTimeout(() => {
      searched = text.trim();
      void handle.update();
    }, 300);
    void handle.update();
  };

  return () => {
    const { selected, cloneDir, onSelect, onCloneDir, unavailableHint } = handle.props;
    if (status.loading && !status.data) return <Loading label="Checking GitHub..." />;
    if (status.error) return <ErrorNote error={status.error} retry={status.reload} what="Could not check GitHub" />;
    const st = status.data;
    if (!st?.loggedIn) {
      return (
        <Notice tone="warn" title={st?.unreachable ? 'GitHub did not answer.' : st?.installed ? 'GitHub is not signed in.' : 'GitHub is not available.'} testId="github-unavailable">
          {st?.error ? <span>{st.error}. </span> : null}
          Cloning uses your own GitHub CLI sign-in, and Drydock stores no token for it. {unavailableHint ?? null}
          {st?.installed && !st.unreachable ? <GitHubSignIn onDone={() => void status.reload()} /> : null}
          {st && !st.installed ? (
            <p style={{ margin: '0' }}>
              <a href="https://cli.github.com" target="_blank" rel="noopener noreferrer" data-rmx-document data-testid="gh-install-link">
                How to install gh
              </a>
            </p>
          ) : null}
        </Notice>
      );
    }
    if (!cloneDir && !askedDir) {
      askedDir = true;
      void s.client.githubCloneDir().then((d) => (d && !handle.props.cloneDir ? onCloneDir(d) : undefined), () => undefined);
    }
    const ownerList = owners.data ?? [];
    const current = owner || ownerList[0] || '';
    const list = repos.data ?? [];
    const targetPath = cloneTarget(cloneDir, selected);
    checkTarget(targetPath);
    const there = target && target.path === targetPath ? target : undefined;
    const known = selected?.projectId ? selected : list.find((r) => r.fullName === selected?.fullName && r.projectId);
    const typedName = typedRepo(typed);
    const onTyped = (v: string) => {
      typed = v;
      const full = typedRepo(v);
      if (full) {
        const [o, n] = full.split('/') as [string, string];
        fromTyped = true;
        onSelect(list.find((r) => r.fullName.toLowerCase() === full.toLowerCase()) ?? { fullName: full, owner: o, name: n, description: '', private: false, archived: false, fork: false, defaultBranch: '', htmlUrl: '', pushedAt: '' });
      } else if (fromTyped) {
        // the field no longer names a repository: the one it named before is not cloned behind the person's back
        fromTyped = false;
        onSelect(undefined);
      }
      void handle.update();
    };
    const listError = owners.error ?? repos.error;
    return (
      <div mix={wrapCss} data-testid="repo-picker">
        <p style={{ margin: '0', color: 'var(--text-muted)', fontSize: 'var(--text-sm)' }}>
          Signed in to GitHub as <strong>{st.login}</strong>.
        </p>
        <Field
          label="Repository (owner/name or URL)"
          hint={typed.trim() && !typedName ? 'Type it as owner/name, or paste its github.com address.' : typedName ? `Clones ${typedName}.` : 'Or choose one from the list below.'}
        >
          {(id) => (
            <input id={id} value={typed} placeholder="acme/billing-api" autocomplete="off" spellcheck={false} data-testid="repo-typed" mix={on('input', (e) => onTyped(e.currentTarget.value))} />
          )}
        </Field>
        <div mix={barCss}>
          <Field label="Owner">
            {(id) => (
              <Select id={id} value={current} disabled={ownerList.length === 0} options={selectValues(ownerList)} onChange={(v) => ((owner = v), void handle.update())} />
            )}
          </Field>
          <Field label="Search repositories">
            {(id) => (
              <div mix={searchCss}>
                <Icon icon={Search} size={14} />
                <input id={id} type="search" value={text} placeholder="Filter by name" autocomplete="off" spellcheck={false} mix={on('input', (e) => onText(e.currentTarget.value))} />
              </div>
            )}
          </Field>
        </div>
        <ErrorNote error={listError} what="Could not list GitHub repositories" />
        {listError ? (
          <div>
            <Button type="button" variant="secondary" size="sm" data-testid="repos-retry" mix={on('click', () => void (owners.error ? owners.reload() : repos.reload()))}>
              Try again
            </Button>
          </div>
        ) : null}
        {repos.loading && list.length === 0 ? <Loading label="Loading repositories..." /> : null}
        {!repos.loading && repos.data && list.length === 0 ? (
          <Empty title="No repositories" icon={Search}>
            <p>{searched ? `Nothing matches "${searched}" for ${current}.` : `${current} has no repositories you can reach.`}</p>
          </Empty>
        ) : null}
        {list.length > 0 ? (
          <div mix={listCss} role="radiogroup" aria-label="Repositories">
            {list.map((r) => (
              <label key={r.fullName} mix={itemCss} data-testid="gh-repo" data-repo={r.fullName}>
                <input type="radio" name={listId} checked={selected?.fullName === r.fullName} mix={on('change', () => ((typed = ''), (fromTyped = false), onSelect(r)))} aria-label={r.fullName} />
                <span style={{ display: 'grid', gap: '2px', minWidth: '0' }}>
                  <span class="top">
                    <span class="n">{r.fullName}</span>
                    {r.private ? (
                      <Badge tone="muted" title="Private repository">
                        <Icon icon={Lock} size={10} /> private
                      </Badge>
                    ) : null}
                    {r.archived ? <Badge tone="warn">archived</Badge> : null}
                    {r.fork ? <Badge tone="muted">fork</Badge> : null}
                  </span>
                  {r.description ? <span class="desc" title={r.description}>{r.description}</span> : null}
                  {r.projectId ? (
                    <span class="desc" data-testid="gh-repo-project">
                      Already a project: {r.project || r.name}
                    </span>
                  ) : null}
                  <span class="when">{pushedAgo(r.pushedAt)}</span>
                </span>
              </label>
            ))}
          </div>
        ) : null}
        {known?.projectId ? (
          <Notice tone="info" testId="repo-is-project">
            {known.fullName} is already a project: {known.project || known.name}.{' '}
            <Button type="button" size="sm" variant="secondary" data-testid="repo-open-project" mix={on('click', () => openProject(known.projectId!))}>
              Open
            </Button>
          </Notice>
        ) : null}
        <Field label="Clone into" hint={targetPath ? <span>Clones to <span mix={targetCss} data-testid="clone-target">{targetPath}</span></span> : 'The folder the repository is cloned into (a new subfolder named after it).'}>
          {(id) => (
            <div mix={folderRow}>
              <input id={id} value={cloneDir} spellcheck={false} autocomplete="off" placeholder="C:\Users\you\source\repos" mix={on('input', (e) => onCloneDir(e.currentTarget.value))} />
              <ChooseFolder title="Choose the folder to clone into" dir={cloneDir} onPick={onCloneDir} testId="clone-choose" />
            </div>
          )}
        </Field>
        {there?.state === 'repository' && !sameRepo(there.origin, selected?.fullName) ? (
          <Notice tone="warn" testId="clone-target-busy">
            Another Git repository{there.origin ? ` (${there.origin})` : ''} is in this folder, so the clone would be refused. Choose another folder to clone into.
          </Notice>
        ) : there?.state === 'repository' ? (
          <Notice tone="info" testId="clone-target-repo">
            Already cloned here{there.branch ? ` (branch ${there.branch})` : ''}.{' '}
            {handle.props.onUseFolder ? (
              <Button type="button" size="sm" variant="secondary" data-testid="clone-use-folder" mix={on('click', () => handle.props.onUseFolder!(there.root || there.path))}>
                Use this folder
              </Button>
            ) : (
              'Choose another folder to clone again.'
            )}
          </Notice>
        ) : there?.state === 'folder' && there.entries > 0 ? (
          <Notice tone="warn" testId="clone-target-busy">
            This folder has files in it, so the clone would be refused. Choose another folder to clone into.
          </Notice>
        ) : there && (there.state === 'no_branch' || there.state === 'not_folder') ? (
          <Notice tone="warn" testId="clone-target-busy">
            {there.message}
          </Notice>
        ) : null}
      </div>
    );
  };
}

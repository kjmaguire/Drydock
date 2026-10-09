// Pick a GitHub repository through the user's own `gh` (Drydock stores no GitHub token): the account and its organizations,
// a search box, the repositories as a list of radio buttons, and the folder to clone into. When gh is missing or not signed
// in, it says so in the core's words. Used by the new-project wizard and by Project settings (attach a repository).

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { Lock, Search } from 'lucide';
import type { GitHubRepo } from '../../api/types.ts';
import { query, services } from '../../state/services.tsx';
import { Badge } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { cloneTarget, pushedAgo } from './wizard.ts';

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

export function RepoPicker(
  handle: Handle<{ selected: GitHubRepo | undefined; cloneDir: string; onSelect: (r: GitHubRepo) => void; onCloneDir: (dir: string) => void; unavailableHint?: RemixNode }>,
) {
  const s = services(handle);
  let owner = '';
  let text = '';
  let searched = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  let askedDir = false;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  const listId = `repos-${handle.id}`;

  const status = query(handle, { load: () => s.client.githubStatus() });
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
    if (status.error) return <ErrorNote error={status.error} what="Could not check GitHub" />;
    const st = status.data;
    if (!st?.loggedIn) {
      return (
        <Notice tone="warn" title={st?.unreachable ? 'GitHub did not answer.' : st?.installed ? 'GitHub is not signed in.' : 'GitHub is not available.'} testId="github-unavailable">
          {st?.error ? <span>{st.error}. </span> : null}
          Drydock uses your own GitHub CLI and stores no GitHub token. {unavailableHint ?? null}
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
    const target = cloneTarget(cloneDir, selected);
    return (
      <div mix={wrapCss} data-testid="repo-picker">
        <p style={{ margin: '0', color: 'var(--text-muted)', fontSize: 'var(--text-sm)' }}>
          Signed in to GitHub as <strong>{st.login}</strong>.
        </p>
        <div mix={barCss}>
          <Field label="Owner">
            {(id) => (
              <select id={id} value={current} disabled={ownerList.length === 0} mix={on('change', (e) => ((owner = e.currentTarget.value), void handle.update()))}>
                {ownerList.map((o) => (
                  <option key={o} value={o} selected={o === current}>
                    {o}
                  </option>
                ))}
              </select>
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
        <ErrorNote error={owners.error ?? repos.error} what="Could not list GitHub repositories" />
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
                <input type="radio" name={listId} checked={selected?.fullName === r.fullName} mix={on('change', () => onSelect(r))} aria-label={r.fullName} />
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
                  <span class="when">{pushedAgo(r.pushedAt)}</span>
                </span>
              </label>
            ))}
          </div>
        ) : null}
        <Field label="Clone into" hint={target ? <span>Clones to <span mix={targetCss} data-testid="clone-target">{target}</span></span> : 'The folder the repository is cloned into (a new subfolder named after it).'}>
          {(id) => <input id={id} value={cloneDir} spellcheck={false} autocomplete="off" placeholder="C:\Users\you\source\repos" mix={on('input', (e) => onCloneDir(e.currentTarget.value))} />}
        </Field>
      </div>
    );
  };
}

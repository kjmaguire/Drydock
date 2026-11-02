// GitHub, one detail on the Integrations page (decision P98, after Apple's System Settings, Linear's account settings and Vercel's Git
// settings; inside the Integrations list since P106, so the list stays beside it): everything Drydock does with GitHub as rows and
// tables. On top, who gh is signed
// in as (github.status: the GitHub CLI's own login, which Drydock uses and never stores) with chips for issue sync, the MCP
// server and the runners, or what is missing and how to fix it. Then Access: the gh sign-in, issue sync (app sync, decision
// P42: connected here once for the workspace with the integrations screen's own ConnectForm and ServiceView, one
// implementation) and GitHub's MCP server for agents (its connector card, decision P43, in a dialog). Then Repositories: each
// project with its repository, the CI state of its default branch and its open pull requests (github.projectRepos). Last the
// CI runners on this PC (ci-runners.tsx, decision P56). A workspace detail: it works with no project.

import { css, navigate, on, type Handle, type RemixNode } from 'remix/component';
import { ExternalLink, FolderGit2, RefreshCw } from 'lucide';
import type { IntegrationKindInfo, IntegrationTestReport, IntegrationView } from '../../api/extTypes.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import type { GitHubStatus, ProjectRepoRow, RepoCI } from '../../api/types.ts';
import { plural } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { query, services, type AnyHandle } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, faint, hueFor, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { actionsEnd, Page, tableCard } from '../../ui/layout.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { SettingRow, SettingsGroup, StatusWord } from '../../ui/master-detail.tsx';
import { t, type Tone } from '../../ui/tokens.ts';
import { ConnectForm, ReportView, ServiceView } from '../integrations/service-detail.tsx';
import { KINDS, serviceState, statusTone, tileState, vendors } from '../integrations/integrations.ts';
import { ConnectorCard } from '../mcp/gallery.tsx';
import { chipCss, CiRunners, sentence } from './ci-runners.tsx';
import { newProjectHref } from '../../shell/new.ts';
import { Switch } from '../../ui/form.tsx';

const innerCss = css({ display: 'grid', gap: t.s6, width: '100%', minWidth: '0' });
const heroCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: `${t.s3} ${t.s4}`,
  minWidth: '0',
  '& p': { margin: '0' },
});
const heroText = css({ display: 'grid', gap: '3px', minWidth: '0', flex: '1 1 260px', '& strong': { fontSize: t.textLg, fontWeight: '600' } });
const heroMark = css({
  display: 'grid',
  placeItems: 'center',
  flex: 'none',
  width: '40px',
  height: '40px',
  borderRadius: t.rFull,
  background: t.warningSoft,
  color: t.warning,
});
const chipsCss = css({ display: 'flex', flexWrap: 'wrap', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });
const noteCss = css({ margin: '0', color: t.textMuted, fontSize: t.textSm });
const tokensCss = css({ marginTop: t.s2 });
const subCss = css({ color: t.textMuted, fontSize: t.textSm });
const lineCss = css({ display: 'inline-flex', flexWrap: 'wrap', alignItems: 'center', gap: `2px ${t.s2}`, minWidth: '0' });
const connectCss = css({ padding: `${t.s3} ${t.s4} ${t.s4}`, borderTop: `1px solid ${t.border}`, display: 'grid', gap: t.s3, minWidth: '0' });
const tableCss = css({
  '& td': { verticalAlign: 'middle' },
  '& td[data-part="repo"]': { fontFamily: t.mono, fontSize: t.textSm, overflowWrap: 'anywhere' },
  '& td[data-part="repo"] a': { display: 'inline-flex', alignItems: 'center', gap: '4px', minHeight: '28px', color: t.text },
  '& td[data-part="open"]': { textAlign: 'right', whiteSpace: 'nowrap' },
  '& .err': { display: 'block', marginTop: '2px', color: t.textMuted, fontSize: t.textSm, overflowWrap: 'anywhere' },
});
const projectCell = css({ display: 'inline-flex', alignItems: 'center', gap: t.s2, '& i': { width: '8px', height: '8px', borderRadius: '2px', flex: 'none' } });

/** The project's colour square, the one the sidebar draws beside its name. */
const projectHue = (id: string): string => `hsl(${hueFor(id)} 62% 62%)`;

/** A chip: a state in words with its dot. */
/** The issue sync's chip: on only when the connection works (the Integrations row's own state), else what needs the person. */
export function syncChip(conn: IntegrationView | undefined): { tone: Tone | 'muted' | 'none'; text: string } {
  if (!conn) return { tone: 'muted', text: 'Issue sync off' };
  const st = serviceState(conn);
  return st.bucket === 'on' ? { tone: 'ok', text: 'Issue sync on' } : { tone: st.tone, text: `Issue sync: ${st.text.toLowerCase()}` };
}

function Chip(handle: Handle<{ tone: Tone | 'muted' | 'none'; text: string; testId: string }>) {
  return () => (
    <li mix={chipCss} data-testid={handle.props.testId}>
      <StatusWord tone={handle.props.tone} text={handle.props.text} />
    </li>
  );
}

/** A state in words with its dot, then what it means, a gap apart (they never touch). */
function StateLine(handle: Handle<{ text: string; tone: Tone | 'muted' | 'none'; children?: RemixNode; testId?: string }>) {
  return () => {
    const { text, tone, children, testId } = handle.props;
    return (
      <span mix={lineCss} data-testid={testId}>
        <StatusWord tone={tone} text={text} />
        {children ? (
          <span mix={subCss} data-part="sub">
            {children}
          </span>
        ) : null}
      </span>
    );
  };
}

/** The sign-in in words, for the hero and the sign-in row. */
export function signInState(st: GitHubStatus | undefined): { text: string; tone: Tone } | undefined {
  if (!st) return undefined;
  if (st.loggedIn) return { text: 'Signed in', tone: 'ok' };
  if (!st.installed) return { text: 'Not installed', tone: 'warn' };
  if (st.unreachable) return { text: 'GitHub did not answer', tone: 'warn' };
  return { text: 'Not signed in', tone: 'warn' };
}

/**
 * The top of the page: who gh is signed in as, with what, and the state of issue sync, the MCP server and the runners as chips;
 * or that gh is missing, signed out or not answering, with the fix and Check again.
 */
function Hero(
  handle: Handle<{ st?: GitHubStatus; loading: boolean; error?: Error; onCheck: () => void; conn?: IntegrationView; card?: McpGalleryCard; runners?: number }>,
) {
  return () => {
    const { st, loading, error, onCheck, conn, card, runners } = handle.props;
    const check = (
      <Button size="sm" icon={RefreshCw} disabled={loading} mix={on('click', onCheck)} title="Ask gh again whether it is signed in">
        {loading ? 'Checking...' : 'Check again'}
      </Button>
    );
    let body: RemixNode = null;
    if (st?.loggedIn) {
      body = (
        <>
          <Avatar id={`gh:${st.login}`} name={st.login} size={40} kind="human" />
          <div mix={heroText}>
            <strong>
              Signed in as @<span data-testid="gh-login">{st.login}</span>
            </strong>
            <span mix={subCss}>
              With the GitHub CLI{st.version ? ` ${st.version}` : ''} · Drydock reads no token on this route
            </span>
          </div>
          <ul mix={chipsCss} aria-label="GitHub at a glance">
            <Chip testId="gh-chip-sync" {...syncChip(conn)} />
            <Chip testId="gh-chip-mcp" tone={card?.status === 'connected' ? 'ok' : 'muted'} text={card?.status === 'connected' ? 'MCP for agents on' : 'MCP for agents off'} />
            {runners !== undefined ? <Chip testId="gh-chip-runners" tone={runners > 0 ? 'accent' : 'muted'} text={runners > 0 ? plural(runners, 'runner') : 'No runners'} /> : null}
          </ul>
        </>
      );
    } else if (st && !st.installed) {
      body = (
        <>
          <span mix={heroMark} aria-hidden="true">
            <Icon icon={FolderGit2} size={18} />
          </span>
          <div mix={heroText}>
            <strong>GitHub CLI not found</strong>
            <p mix={subCss}>
              {/* the install steps are said here once (the connect form below leaves them to this hero); the core's reason only when it adds something */}
              {st.error && !/not installed/i.test(st.error) ? `${st.error} ` : ''}
              Install it from{' '}
              <a href="https://cli.github.com" target="_blank" rel="noopener noreferrer" data-rmx-document>
                cli.github.com <Icon icon={ExternalLink} size={11} />
              </a>{' '}
              and run <code>gh auth login</code>. Pull requests and GitHub repositories need it.
            </p>
          </div>
          {check}
        </>
      );
    } else if (st) {
      body = (
        <>
          <span mix={heroMark} aria-hidden="true">
            <Icon icon={FolderGit2} size={18} />
          </span>
          <div mix={heroText}>
            <strong>{st.unreachable ? 'GitHub did not answer in time.' : 'gh is not signed in.'}</strong>
            <p mix={subCss}>
              {/* the core's reason, unless it only says what the heading says */}
              {st.error && !/not signed in/i.test(st.error) ? `${st.error.replace(/\.$/, '')}. ` : ''}
              {st.unreachable ? 'gh may well be signed in; check again in a moment.' : (
                <>
                  Run <code>gh auth login</code> in a terminal: gh&apos;s own credentials never pass through Drydock.
                </>
              )}
            </p>
          </div>
          {check}
        </>
      );
    }
    return (
      <section mix={heroCss} aria-label="Your sign-in" data-testid="gh-signin-state" data-signed-in={st ? String(!!st.loggedIn) : undefined}>
        <ErrorNote error={error} what="Could not check gh" />
        {loading && !st ? <Loading label="Checking gh" /> : null}
        {body}
      </section>
    );
  };
}

/** How many of the project's agents the GitHub MCP server is granted to, across the projects that have it. */
function useGrants(handle: AnyHandle, card: () => McpGalleryCard | undefined) {
  const s = services(handle);
  return query(handle, {
    enabled: () => !!card()?.libraryId && (card()?.projects.length ?? 0) > 0,
    key: () => [card()?.libraryId, card()?.projects],
    when: (e) => e.type.startsWith('mcp.server'),
    load: async () => {
      const c = card()!;
      const lists = await Promise.all(c.projects.map((p) => s.client.mcpServerList(p)));
      return lists.flat().filter((x) => x.id === c.libraryId).reduce((n, x) => n + x.agents.length, 0);
    },
  });
}

/**
 * Access: the gh sign-in, issue sync and the MCP server for agents, one row each (label, state, control). Issue sync connects
 * inline when it is not connected; Manage opens the connection's own panel, and the MCP server's card opens in a dialog.
 */
function Access(
  handle: Handle<{
    st?: GitHubStatus;
    ghLoading: boolean;
    onCheck: () => void;
    conns?: IntegrationView[];
    connsError?: Error;
    onConnsChanged: () => void;
    info?: IntegrationKindInfo;
    onKindsChanged: () => void;
    cards: McpGalleryCard[];
    galleryLoading: boolean;
    galleryError?: Error;
    onGalleryChanged: () => void;
  }>,
) {
  let manage = false; // the issue sync connection's panel is open
  let mcpOpen = false;
  let report: IntegrationTestReport | undefined;
  const grants = useGrants(handle, () => handle.props.cards[0]);
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  return () => {
    const { st, ghLoading, onCheck, conns, connsError, onConnsChanged, info, onKindsChanged, cards, galleryLoading, galleryError, onGalleryChanged } = handle.props;
    const sign = signInState(st);
    const conn = conns ? tileState(conns, 'github') : undefined;
    const kind = KINDS.find((k) => k.kind === 'github')!;
    const card = cards[0];
    const assigned = (conn?.assignments ?? []).length;
    const method = conn ? conn.identity?.method || (conn.authKind === 'gh' ? 'uses the gh sign-in' : conn.authKind) : '';
    const writes = card ? card.tools.filter((x) => !card.readOnlyTools.includes(x.name)).length : 0;
    const mcpState: Record<string, { text: string; tone: Tone }> = {
      connected: { text: 'Connected', tone: 'ok' },
      not_connected: { text: 'Not connected', tone: 'muted' },
      needs_sign_in: { text: 'Needs sign-in', tone: 'warn' },
      needs_token: { text: 'Needs a token', tone: 'warn' },
      error: { text: 'Error', tone: 'err' },
    };
    const ms = card ? (mcpState[card.status] ?? mcpState.error!) : undefined;
    return (
      <SettingsGroup title="Access" testId="gh-access">
        <SettingRow
          label="GitHub CLI sign-in"
          testId="access-gh"
          control={
            // the hero above has its own Check again while gh is missing or signed out: one button on the page, not two
            !st || st.loggedIn ? (
              <Button size="sm" variant="ghost" icon={RefreshCw} disabled={ghLoading} mix={on('click', onCheck)} title="Ask gh again whether it is signed in">
                {ghLoading ? 'Checking...' : 'Check again'}
              </Button>
            ) : null
          }
        >
          {sign ? (
            <StateLine text={sign.text} tone={sign.tone} testId="gh-state">
              {st?.loggedIn ? 'pushes branches, opens pull requests, reads CI' : 'pull requests and CI need it'}
            </StateLine>
          ) : (
            <span mix={subCss}>Checking...</span>
          )}
        </SettingRow>

        <SettingRow
          label="Issue sync"
          testId="access-sync"
          control={
            conn ? (
              <Button size="sm" variant="ghost" data-testid="sync-manage" mix={on('click', () => set(() => (manage = true)))}>
                Manage
              </Button>
            ) : null
          }
        >
          {connsError ? <ErrorNote error={connsError} what="Issue sync" /> : null}
          {!conns && !connsError ? <span mix={subCss}>Checking...</span> : null}
          {conn ? (
            <StateLine text={sentence(syncStateText(conn))} tone={statusTone(conn.status)} testId="sync-state">
              {method} · {assigned === 0 ? 'no project yet' : plural(assigned, 'project')}
            </StateLine>
          ) : conns ? (
            <StateLine text="Off" tone="muted" testId="sync-state">
              a repository&apos;s issues into the projects you assign
            </StateLine>
          ) : null}
        </SettingRow>
        {conns && !conn ? (
          <div mix={connectCss} data-testid="sync-connect">
            <ConnectForm kind="github" info={info} ghStateAbove onDone={(r) => ((report = r.test), onConnsChanged())} onKindsChanged={onKindsChanged} />
          </div>
        ) : null}
        {report ? (
          <div mix={connectCss}>
            <ReportView r={report} />
          </div>
        ) : null}

        <SettingRow
          label="MCP server for agents"
          testId="access-mcp"
          control={
            card ? (
              <Button size="sm" variant={card.status === 'connected' ? 'ghost' : 'secondary'} data-testid="mcp-open" mix={on('click', () => set(() => (mcpOpen = true)))}>
                {card.status === 'connected' ? 'Manage' : 'Connect'}
              </Button>
            ) : null
          }
        >
          {galleryError ? <ErrorNote error={galleryError} what="MCP servers" /> : null}
          {galleryLoading && !card && !galleryError ? <span mix={subCss}>Checking...</span> : null}
          {card && ms ? (
            <StateLine text={ms.text} tone={ms.tone} testId="mcp-state">
              {[
                card.official ? 'Official' : '',
                plural(card.tools.length, 'tool'),
                writes > 0 ? 'every write waits for you' : '',
                card.status === 'connected' && grants.data !== undefined ? `granted to ${plural(grants.data, 'agent')}` : '',
                card.status === 'connected' && card.projects.length === 0 ? 'in no project yet' : '',
              ]
                .filter(Boolean)
                .join(' · ')}
            </StateLine>
          ) : !galleryLoading && !galleryError ? (
            <span mix={subCss}>Not in this build&apos;s list of MCP servers.</span>
          ) : null}
        </SettingRow>

        <Dialog open={manage && !!conn} title="Issue sync" wide onClose={() => set(() => (manage = false))}>
          {manage && conn ? <ServiceView kind={kind} info={info} conn={conn} hasAgentTools={false} onChanged={onConnsChanged} onKindsChanged={onKindsChanged} open={(href) => void navigate(href)} /> : null}
        </Dialog>
        <Dialog open={mcpOpen && !!card} title="MCP server for agents" onClose={() => set(() => (mcpOpen = false))}>
          {mcpOpen ? (
            <>
              <SettingsGroup label="GitHub's MCP server">
                {cards.map((c) => (
                  <ConnectorCard key={c.id} card={c} onChanged={onGalleryChanged} />
                ))}
              </SettingsGroup>
              <p mix={noteCss}>Grant it to agents on a project&apos;s Integrations page. Tools not marked read-only wait for your approval.</p>
            </>
          ) : null}
        </Dialog>
      </SettingsGroup>
    );
  };
}

const CI: Record<RepoCI, { text: string; tone: Tone }> = {
  passing: { text: 'Passing', tone: 'ok' },
  failing: { text: 'Failing', tone: 'err' },
  running: { text: 'Running', tone: 'info' },
  none: { text: 'No checks', tone: 'muted' },
};

/** The table's own words for the GraphQL fields GitHub may refuse, so the note names the column, not the query. */
const REFUSED_WORDS: Record<string, string> = { statusCheckRollup: 'the CI state', pullRequests: 'the open pull requests', defaultBranchRef: 'the default branch' };

/**
 * What GitHub refused, in the table's words: "statusCheckRollup: Resource not accessible by personal access token" (the core
 * passes GitHub's field names on, several joined by "; ") reads "the CI state: Resource not accessible by personal access
 * token"; a field the table has no words for keeps its name, and an error that named no field is passed on as it is.
 */
export function refusedWords(incomplete: string): string {
  return incomplete
    .split('; ')
    .map((part) => {
      const i = part.indexOf(': ');
      if (i < 0) return part;
      const field = part.slice(0, i);
      return `${REFUSED_WORDS[field] ?? field}${part.slice(i)}`;
    })
    .join('; ');
}

/**
 * The CI cell of a row: the state of the default branch's latest commit, or why there is none. When GitHub refused part of the
 * answer (incomplete), what it refused is said under the state, so "No checks" or "None open" never stands alone as an answer
 * GitHub did not give. A row that could not be read says so with the other program's own words after the state: gh's or
 * GitHub's for `error`, git's or the OS's for `unreadable`; the words are a fragment, so nothing is put in front of them.
 */
function ciCell(r: ProjectRepoRow): RemixNode {
  switch (r.state) {
    case 'github': {
      const c = CI[r.ci ?? 'none'] ?? CI.none;
      return (
        <>
          <StateLine text={c.text} tone={c.tone}>
            {r.defaultBranch ? `on ${r.defaultBranch}` : null}
          </StateLine>
          {r.incomplete ? (
            <span class="err" data-testid="repo-incomplete">
              GitHub refused part of the answer: {refusedWords(r.incomplete)}
            </span>
          ) : null}
        </>
      );
    }
    case 'needs_gh':
      return <StatusWord tone="none" text="Needs gh" />;
    case 'error':
    case 'unreadable':
      return (
        <>
          <StatusWord tone="err" text={r.state === 'error' ? 'Could not read' : 'Not readable'} />
          <span class="err" data-testid="repo-error">
            {r.error}
          </span>
        </>
      );
  }
  return <span mix={faint}>Not on GitHub</span>;
}

/**
 * The Repository cell of a row: owner/name, a link to the repository on github.com when its address is safe; or, in words, why
 * there is none. The state decides, not whether a name came along.
 */
function repoCell(r: ProjectRepoRow): RemixNode {
  switch (r.state) {
    case 'no_repo':
      return <span mix={muted}>No repository</span>;
    case 'unreadable':
      return <span mix={muted}>Folder not readable</span>;
    case 'local':
      return <span mix={muted}>Local folder</span>;
  }
  const href = r.url ? safeHref(r.url) : undefined;
  return r.repo && href ? (
    <a href={href} target="_blank" rel="noopener noreferrer" data-rmx-document data-testid="repo-link" title={`${r.repo} on github.com`}>
      {r.repo}
      <Icon icon={ExternalLink} size={11} />
    </a>
  ) : (
    <span mix={mono}>{r.repo}</span>
  );
}

/**
 * Whether the row's project has a GitHub repository: read (github), refused by GitHub (error) or waiting for the GitHub CLI
 * (needs_gh). The state says so, never whether `repo` happens to be set, so a core that one day names the folder of an
 * unreadable row cannot send Open to pull requests that are not there.
 */
const onGitHub = (r: ProjectRepoRow): boolean => r.state === 'github' || r.state === 'error' || r.state === 'needs_gh';

/**
 * Keeping project folders up to date (decision P143): the background check fetches every project's remote when the project opens
 * and every 15 minutes, so its Work page says how far behind it is; files change only with Sync now. One switch turns it off.
 */
function FolderSync(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.repoSyncSettings(), when: (e) => e.type === 'repo.syncSettings' });
  let error: unknown;
  const set = async (auto: boolean) => {
    error = undefined;
    try {
      q.set(await s.client.repoSyncSettingsSet({ auto }));
    } catch (e) {
      error = e;
      void handle.update();
    }
  };
  return () => (
    <SettingsGroup title="Project folders" testId="gh-folder-sync">
      <SettingRow
        label="Check for new commits"
        testId="folder-sync-auto"
        control={q.data ? <Switch label={<span class="sr-only">Check for new commits</span>} checked={q.data.auto} onChange={(v) => void set(v)} /> : null}
      >
        <ErrorNote error={q.error ?? error} what="Could not change the check" />
        <span mix={subCss}>When a project opens and every 15 minutes; files change only with Sync now on its Work page.</span>
      </SettingRow>
    </SettingsGroup>
  );
}

/**
 * Repositories: one row per project with its repository, the CI state of the default branch and the open pull requests, every
 * cell drawn from the row's state (the core says what each row is). A repository opens on github.com (the system browser); Open
 * goes to the project's Pull requests when it has a GitHub repository, else to its Overview. Refresh asks GitHub again.
 */
function Repositories(handle: Handle) {
  const s = services(handle);
  let refresh = false;
  const q = query(handle, {
    load: () => s.client.githubProjectRepos(refresh),
    when: (e) => e.type.startsWith('project.') || e.type === 'github.cloned',
    debounceMs: 300,
  });
  const again = async () => {
    refresh = true;
    try {
      await q.reload();
    } finally {
      refresh = false;
    }
  };
  return () => {
    const rows = q.data?.rows ?? [];
    return (
      <SettingsGroup
        title="Repositories"
        testId="gh-repos"
        actions={
          <span mix={actionsEnd}>
            <span mix={faint}>From your projects</span>
            <Button size="sm" variant="ghost" icon={RefreshCw} disabled={q.loading} data-testid="repos-refresh" mix={on('click', () => void again())}>
              {q.loading && q.data ? 'Refreshing...' : 'Refresh'}
            </Button>
            {/* a repository of GitHub into a folder of this PC, as a new project: New project's Clone from GitHub, with gh (P143) */}
            <a
              href={newProjectHref('clone')}
              mix={buttonStyle('secondary', 'sm')}
              data-testid="repos-clone"
              title="New project: pick a GitHub repository and the folder to clone it into, with your gh sign-in"
            >
              <Icon icon={FolderGit2} size={14} />
              Clone a repository
            </a>
          </span>
        }
        bare
      >
        <ErrorNote error={q.error} what="Could not read the repositories" />
        {q.loading && !q.data ? <Loading label="Reading your repositories" /> : null}
        {q.data && rows.length === 0 ? (
          <div mix={tableCard} data-testid="repos-empty">
            <Empty title="No projects yet" icon={FolderGit2}>
              <p>A project&apos;s repository shows here with its CI and pull requests.</p>
              <a href={newProjectHref()} mix={buttonStyle('secondary', 'sm')}>
                New project
              </a>
            </Empty>
          </div>
        ) : null}
        {rows.length > 0 ? (
          <div mix={tableCard}>
            <table mix={tableCss} aria-label="Repositories">
              <thead>
                <tr>
                  <th scope="col">Project</th>
                  <th scope="col">Repository</th>
                  <th scope="col">CI</th>
                  <th scope="col">Pull requests</th>
                  <th scope="col">
                    <span class="sr-only">Open</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const gh = onGitHub(r);
                  return (
                    <tr key={r.projectId} data-testid="repo-row" data-project={r.projectId} data-state={r.state} data-ci={r.ci}>
                      <td>
                        <span mix={projectCell}>
                          <i aria-hidden="true" style={{ background: projectHue(r.projectId) }} />
                          {r.project}
                        </span>
                      </td>
                      <td data-part="repo">{repoCell(r)}</td>
                      <td data-testid="repo-ci">{ciCell(r)}</td>
                      <td data-testid="repo-prs">{r.state === 'github' ? (r.openPullRequests > 0 ? `${r.openPullRequests} open` : 'None open') : <span mix={faint}>—</span>}</td>
                      <td data-part="open">
                        <a
                          href={gh ? routes.pulls.href() : routes.home.href()}
                          mix={[buttonStyle('ghost', 'sm'), on('click', () => s.workspace.selectProject(r.projectId))]}
                          aria-label={gh ? `Open the pull requests of ${r.project}` : `Open ${r.project}`}
                          data-testid="repo-open"
                        >
                          Open
                        </a>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
      </SettingsGroup>
    );
  };
}

/** The issue sync's state in words: who it reads as once connected, else what is wrong. */
function syncStateText(c: IntegrationView | undefined): string {
  if (!c) return 'not connected';
  const account = c.identity?.account?.trim();
  if (account) return `connected as ${account}`;
  return c.status === 'ok' ? 'connected' : c.status === 'untested' ? 'connected, not checked yet' : `connected (${c.status})`;
}

/** The GitHub settings page. */
/**
 * GitHub's detail on the Integrations page (/integrations/github): the sign-in, issue sync, the MCP server, repositories and CI
 * runners. It opens in the page's list and detail, so the list stays beside it (it used to be a page of its own; /github opens it).
 */
export function GitHubDetail(handle: Handle) {
  const s = services(handle);
  const gh = query(handle, { load: () => s.client.githubStatus() });
  const conns = query(handle, { load: () => s.client.integrationList(), when: (e) => e.type.startsWith('integration.') });
  const kinds = query(handle, { load: () => s.client.integrationKinds() });
  const gallery = query(handle, { load: () => s.client.mcpGallery(), when: (e) => e.type.startsWith('mcp.server') });
  const runners = query(handle, { load: () => s.client.runnersGet(), when: (e) => e.type === 'runners.changed', debounceMs: 150 });
  const check = () => void gh.reload();
  return () => {
    const cards = vendors(KINDS, gallery.data ?? []).find((v) => v.key === 'github')?.cards ?? [];
    const conn = conns.data ? tileState(conns.data, 'github') : undefined;
    return (
      <div mix={innerCss} data-testid="github-page">
        <Hero st={gh.data} loading={gh.loading} error={gh.error} onCheck={check} conn={conn} card={cards[0]} runners={runners.data?.slots.length} />
        <div>
          <Access
            st={gh.data}
            ghLoading={gh.loading}
            onCheck={check}
            conns={conns.data}
            connsError={conns.error ?? kinds.error}
            onConnsChanged={() => void conns.reload()}
            info={(kinds.data ?? []).find((k) => k.kind === 'github')}
            onKindsChanged={() => void kinds.reload()}
            cards={cards}
            galleryLoading={gallery.loading}
            galleryError={gallery.error}
            onGalleryChanged={() => void gallery.reload()}
          />
          <p mix={[noteCss, tokensCss]} data-testid="gh-tokens">
            Issue sync by device code and the MCP server keep their own token in the Windows credential store, never shown.
          </p>
        </div>
        <Repositories />
        <FolderSync />
        <CiRunners />
      </div>
    );
  };
}

/** Settings > GitHub (/github, decision P140): the same detail as Integrations' GitHub row, as a section of its own. */
export function GitHubScreen(_handle: Handle) {
  return () => (
    <Page title="GitHub" icon={FolderGit2} testId="github-section">
      <GitHubDetail />
    </Page>
  );
}

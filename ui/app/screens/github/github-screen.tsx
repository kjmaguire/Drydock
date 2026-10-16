// GitHub, one settings page (on the owner's request, 2026-10-16): everything Drydock does with GitHub and the sign-in it does
// it with. Your gh sign-in (github.status: the GitHub CLI's own login, which Drydock uses and never stores), what Drydock does
// with it in plain words (docs/github.md), the GitHub connection for app sync and the GitHub MCP server for agents (the same
// panel the GitHub tile on Integrations opens: VendorDetail, one implementation), and which repositories have CI runners on
// this PC (runners.get; adding and managing them stays on Connections). A workspace page: it works with no project.

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, GitPullRequest, RefreshCw } from 'lucide';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, listCard, Page, Section } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { runnerStateText } from '../connections/ci-runners.tsx';
import { VendorDetail } from '../integrations/integrations-screen.tsx';

const columnCss = css({ display: 'grid', gap: t.s8, minWidth: '0' });
const cardCss = css({ display: 'grid', gap: t.s3, minWidth: '0', '& p': { margin: '0' } });
const toolbar = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });
const doesCss = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  paddingLeft: '20px',
  fontSize: t.textMd,
  '& li': { lineHeight: '1.5' },
  '& strong': { fontWeight: '600' },
});
const slotRow = css({ flexWrap: 'wrap', '& .repo': { fontWeight: '600' }, '& .kinds': { display: 'flex', gap: t.s2, flexWrap: 'wrap', marginLeft: 'auto' } });

const KIND_NAME = { windows: 'Windows', linux: 'Linux' } as const;

/** The gh sign-in on this PC: installed and signed in, or what is missing, with Check again. */
function SignIn(handle: Handle) {
  const s = services(handle);
  const gh = query(handle, { load: () => s.client.githubStatus() });
  return () => {
    const st = gh.data;
    return (
      <Section
        title="Your sign-in"
        label="Your sign-in"
        testId="gh-signin"
        description="Drydock works on GitHub through the GitHub CLI (gh) that you installed and signed in to. It never reads or stores a GitHub token: there is nothing of GitHub's to keep in its credential store."
        actions={
          <Button size="sm" icon={RefreshCw} disabled={gh.loading} mix={on('click', () => gh.reload())} title="Ask gh again whether it is signed in">
            {gh.loading ? 'Checking...' : 'Check again'}
          </Button>
        }
      >
        <div mix={[card, cardCss]} data-testid="gh-signin-state" data-signed-in={st ? String(!!st.loggedIn) : undefined}>
          <ErrorNote error={gh.error} what="Could not check gh" />
          {gh.loading && !st ? <Loading label="Checking gh" /> : null}
          {st?.loggedIn ? (
            <Notice tone="ok" role="status">
              gh is signed in as <strong data-testid="gh-login">{st.login}</strong>. Drydock pushes branches, opens pull requests and reads CI with this sign-in.
            </Notice>
          ) : st && !st.installed ? (
            <Notice tone="warn" role="status" title="The GitHub CLI (gh) is not installed.">
              {st.error ? `${st.error} ` : ''}
              Install it from{' '}
              <a href="https://cli.github.com" target="_blank" rel="noopener noreferrer" data-rmx-document>
                cli.github.com <Icon icon={ExternalLink} size={11} />
              </a>{' '}
              and run <code>gh auth login</code>. Until then a project can still work on a local folder; pull requests and GitHub repositories need gh.
            </Notice>
          ) : st?.unreachable ? (
            <Notice tone="warn" role="status" title="GitHub did not answer in time.">
              {st.error ? `${st.error} ` : ''}gh may well be signed in; check again in a moment.
            </Notice>
          ) : st ? (
            <Notice tone="warn" role="status" title="gh is not signed in.">
              {st.error ? `${st.error} ` : ''}
              Run <code>gh auth login</code> in a terminal. Drydock has no sign-in button of its own, so your credentials never pass through it.
            </Notice>
          ) : null}
          <p mix={faint}>
            A sign-in is checked by asking GitHub who it is (gh api /user). Scopes are not checked: gh asks for repo and read:org when you sign in, and that is what Drydock needs.
          </p>
        </div>
      </Section>
    );
  };
}

/** What Drydock does with GitHub, in plain words (docs/github.md), so the page reads as a whole and not as a pile of controls. */
function WhatItDoes() {
  return () => (
    <Section title="What Drydock does with GitHub" label="What Drydock does with GitHub" testId="gh-about">
      <div mix={[card, cardCss]}>
        <ul mix={doesCss}>
          <li>
            <strong>Pushes a task's branch and opens its pull request</strong> with your gh sign-in, in a project whose delivery mode is pull requests. The description comes from the task:
            its summary, acceptance criteria, evidence and the reviewers' verdicts. A retry finds the open pull request of the branch instead of opening a second one.
          </li>
          <li>
            <strong>Waits for CI</strong> on the pushed commit and hands a failing job's log back to the worker, which fixes it and pushes again. No CI reported is a question for you in
            Needs you, never a pass; a repository without CI is something only you can declare, in Project settings.
          </li>
          <li>
            <strong>Reads the review</strong> on the pull request and feeds a collaborator's requested changes back to the task.
          </li>
          <li>
            <strong>Merges only after you approve</strong>, through GitHub, with the project's merge method, and only while the pull request's head is still the commit you reviewed.
            Branch protection is respected and never bypassed; auto-merge is never enabled.
          </li>
          <li>
            <strong>Imports a repository's issues</strong> as tasks of a project once you assign the GitHub connection to it (below, then on the project's Integrations page).
          </li>
          <li>
            <strong>Gives agents GitHub's tools</strong> through the GitHub MCP server, once you connect it here and grant it to a project's agents; every tool that writes waits for your
            approval, call by call.
          </li>
          <li>
            <strong>Runs your private repositories' CI on this PC</strong> when you add a runner on Connections. Runners only run jobs and report to GitHub.
          </li>
          <li>
            <strong>Never reads or stores a GitHub token.</strong> Every call is gh with your own sign-in, and everything read from GitHub (issues, comments, logs) is treated as untrusted
            text.
          </li>
        </ul>
      </div>
    </Section>
  );
}

/** Which repositories have a CI runner on this PC, each runner's state in a word or two, and the way to Connections to manage them. */
function Runners(handle: Handle) {
  const s = services(handle);
  const v = query(handle, { load: () => s.client.runnersGet(), when: (e) => e.type === 'runners.changed', debounceMs: 150 });
  return () => {
    const slots = v.data?.slots ?? [];
    const repos = Array.from(new Set(slots.map((x) => x.repo)));
    return (
      <Section
        title="CI runners"
        label="CI runners"
        testId="gh-runners"
        description="GitHub Actions self-hosted runners for your private repositories, kept by Drydock on this PC. They are added, switched on or off and removed on Connections."
        actions={
          <a href={routes.connections.href()} mix={buttonStyle('secondary', 'sm')} data-testid="gh-runners-link">
            Manage runners on Connections
          </a>
        }
      >
        <ErrorNote error={v.error} what="CI runners" />
        {v.loading && !v.data ? <Loading label="Reading the runners..." /> : null}
        {v.data && repos.length === 0 ? (
          <div mix={[card, cardCss]}>
            <p mix={muted} data-testid="gh-runners-none">
              No repository has a runner yet. A runner takes one job of your private repository at a time on this PC; add one under CI runners on Connections.
            </p>
          </div>
        ) : null}
        {repos.length > 0 ? (
          <ul mix={listCard} aria-label="Repositories with runners">
            {repos.map((repo) => (
              <li key={repo} mix={slotRow} data-testid="gh-runner-repo" data-repo={repo}>
                <span class="repo" mix={mono}>
                  {repo}
                </span>
                <span class="kinds">
                  {slots
                    .filter((x) => x.repo === repo)
                    .map((x) => {
                      const st = runnerStateText(x);
                      return (
                        <Badge key={x.kind} tone={st.tone} title={x.reason}>
                          {KIND_NAME[x.kind]}: {st.text}
                        </Badge>
                      );
                    })}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
      </Section>
    );
  };
}

/** The GitHub settings page. */
export function GitHubScreen() {
  return () => (
    <Page title="GitHub" icon={GitPullRequest} testId="github-page" subtitle="What Drydock does on GitHub, and the sign-in it does it with">
      <div mix={columnCss}>
        <SignIn />
        <WhatItDoes />
        <Section
          title="Connection and agent tools"
          label="Connection and agent tools"
          testId="gh-connection"
          description="The GitHub connection for app sync (a repository's issues into the projects you assign) and GitHub's MCP server for agents. This is the GitHub tile of the Integrations page, in one place with the rest."
        >
          <VendorDetail vendor="github" />
          <p mix={[muted, toolbar]}>
            <a href={routes.integrations.href()} data-testid="gh-integrations-link">
              All integrations
            </a>
          </p>
        </Section>
        <Runners />
      </div>
    </Page>
  );
}

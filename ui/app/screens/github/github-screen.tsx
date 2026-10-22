// GitHub, one settings page (on the owner's request, 2026-10-16): everything Drydock does with GitHub and the sign-in it does
// it with. Your gh sign-in (github.status: the GitHub CLI's own login, which Drydock uses and never stores), what Drydock does
// with it (the Guide's GitHub topic explains the rest), the GitHub connection for app sync and the GitHub MCP server for agents (the same
// panel the GitHub tile on Integrations opens: VendorDetail, one implementation), and the CI runners on this PC (ci-runners.tsx,
// decision P56: added, switched and removed here; on the owner's request, 2026-10-22, they moved here from Connections). A
// workspace page: it works with no project.

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, GitPullRequest, RefreshCw } from 'lucide';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { VendorDetail } from '../integrations/integrations-screen.tsx';
import { CiRunners } from './ci-runners.tsx';

const columnCss = css({ display: 'grid', gap: t.s8, minWidth: '0' });
const cardCss = css({ display: 'grid', gap: t.s3, minWidth: '0', '& p': { margin: '0' } });
const toolbar = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });

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
        description="Uses the gh sign-in you made. On this route Drydock reads and stores no GitHub token; a connection or MCP server below keeps its own, in the Windows credential store, never shown."
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
            <Notice tone="warn" role="status" title="GitHub CLI not found">
              {/* the core's own words say the same install steps in the connection panel below; here only a reason that adds something */}
              {st.error && !/not installed/i.test(st.error) ? `${st.error} ` : ''}
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
              Run <code>gh auth login</code> in a terminal. Drydock has no sign-in button for gh, so gh&apos;s own credentials never pass through it.
            </Notice>
          ) : null}
        </div>
      </Section>
    );
  };
}

/** The GitHub settings page. */
export function GitHubScreen() {
  return () => (
    <Page title="GitHub" icon={GitPullRequest} testId="github-page" subtitle="Your gh sign-in, the connection and the CI runners" guide="github">
      <div mix={columnCss}>
        <SignIn />
        <Section
          title="Connection and agent tools"
          label="Connection and agent tools"
          testId="gh-connection"
          description="App sync (a repository's issues into the projects you assign) and GitHub's MCP server for agents."
        >
          <VendorDetail vendor="github" />
          <p mix={[muted, toolbar]}>
            <a href={routes.integrations.href()} mix={buttonStyle('ghost', 'sm')} data-testid="gh-integrations-link">
              All integrations
            </a>
          </p>
        </Section>
        <CiRunners />
      </div>
    </Page>
  );
}

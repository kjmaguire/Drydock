// GitHub's Sign in (F4): the person's own `gh auth login` runs in a terminal here (github.signIn); Drydock reads only its exit
// code, never what gh stores. When it ends the core asks gh again and publishes github.sign_in_finished, which makes every
// page that shows gh's state read it again; onDone lets the page that holds this button read at once. Used by the GitHub
// page's hero and the clone picker.

import { css, on, type Handle } from 'remix/component';
import { Terminal } from 'lucide';
import { services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { TerminalHost } from '../dock/terminal-panel.tsx';

const boxCss = css({ display: 'grid', gap: t.s2, minWidth: '0', '& p': { margin: '0' }, '& .quiet': { color: t.textMuted, fontSize: t.textSm } });
const rowCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2 });
const termCss = css({ display: 'grid', gap: t.s2, '& > div:last-child': { height: '220px', minHeight: '0' } });

/** What the sign-in line says once the terminal is open. */
export function ghSignInLine(ended: number | undefined): string {
  if (ended === undefined) return 'gh auth login is running: answer it here.';
  if (ended === 0) return 'The sign-in finished. Checking gh again...';
  return `The sign-in ended with code ${ended}. Read what it printed above, then try again.`;
}

export function GitHubSignIn(handle: Handle<{ onDone?: () => void }>) {
  const s = services(handle);
  let term: string | undefined;
  let ended: number | undefined;
  let starting = false;
  let error: Error | undefined;
  const upd = (f: () => void) => (f(), void handle.update());
  const start = async () => {
    upd(() => ((starting = true), (error = undefined)));
    try {
      const info = await s.client.githubSignIn();
      if (info.id !== term) ended = undefined;
      term = info.id;
    } catch (e) {
      error = e as Error;
    } finally {
      upd(() => (starting = false));
    }
  };
  handle.signal.addEventListener('abort', () => {
    if (term && ended === undefined) void s.client.terminalClose(term).catch(() => {});
  });
  return () => (
    <div mix={boxCss} data-testid="gh-sign-in-box">
      {term ? (
        <div mix={termCss} data-testid="gh-sign-in-terminal">
          <p class="quiet" data-testid="gh-sign-in-state">
            {ghSignInLine(ended)}
          </p>
          {ended !== undefined && ended !== 0 ? (
            <div mix={rowCss}>
              <Button size="sm" variant="secondary" disabled={starting} data-testid="gh-sign-in-again" mix={on('click', () => void start())}>
                Try again
              </Button>
            </div>
          ) : null}
          <TerminalHost
            key={term}
            id={term}
            program="gh auth login"
            onExit={(code) => {
              upd(() => (ended = code));
              handle.props.onDone?.();
            }}
          />
        </div>
      ) : (
        <div mix={rowCss}>
          <Button size="sm" variant="primary" disabled={starting} data-testid="gh-sign-in" mix={on('click', () => void start())}>
            {starting ? 'Starting...' : 'Sign in'}
          </Button>
          <span class="quiet">
            <Icon icon={Terminal} size={12} /> Runs <code>gh auth login</code> here
          </span>
        </div>
      )}
      <ErrorNote error={error} what="Could not start the sign-in" />
    </div>
  );
}

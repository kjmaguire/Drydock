// The repository configuration guard (docs/security.md s3, internal/git/guard.go) as the person sees it. Drydock refuses to
// run git in a project's repository whose own git configuration, hooks or info/attributes changed since a person last
// trusted them: a hook, filter or command planted there (an agent can write the shared .git from a task's worktree) would
// run inside Drydock's own git calls. The core names the changed items (config keys, "hooks/<file>", "info/attributes"),
// never their values. Two places show it:
// - outside a run, the project banner (shell/repo-config-banner.tsx) from project.gitConfig, trusted with
//   project.trustGitConfig and the fingerprint that was shown;
// - inside a run, the approval with gate "repo_config_changed" on the task page (RepoConfigGatePanel); approving it
//   trusts exactly the state it names.
// Nothing is ever trusted without a person's click on a button bound to the fingerprint (or the request id) shown.

import { css, on, type Handle } from 'remix/component';
import { Octagon, ShieldCheck } from 'lucide';
import type { PendingRequest } from '../../api/types.ts';
import { Button } from '../../ui/button.tsx';
import { formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';

/** The payload of the approval a git.ConfigChangedError raises in a run (internal/git/guard.go Gate). */
export interface RepoConfigPayload {
  gate: 'repo_config_changed';
  /** The changed items: names only, never values. */
  changed: string[];
  /** The state approving trusts. */
  fingerprint: string;
  /** The repository's root folder. */
  root?: string;
}

export const REPO_CONFIG_GATE = 'repo_config_changed';

export function repoConfigPayload(p: PendingRequest): RepoConfigPayload | undefined {
  const v = p.spec.payload as Partial<RepoConfigPayload> | null | undefined;
  if (!v || v.gate !== REPO_CONFIG_GATE) return undefined;
  const changed = Array.isArray(v.changed) ? v.changed.filter((x): x is string => typeof x === 'string') : [];
  return { gate: REPO_CONFIG_GATE, changed, fingerprint: typeof v.fingerprint === 'string' ? v.fingerprint : '', root: typeof v.root === 'string' ? v.root : undefined };
}

/** The plain-words explanation, naming the changed items. */
export function repoConfigText(changed: readonly string[]): string {
  const list = changed.length > 0 ? ` (${changed.join(', ')})` : '';
  return (
    `The repository's git configuration changed${list}. Drydock does not run git there until you review it: a hook, filter ` +
    `or command planted there would run in Drydock's own git calls. Trust it only if you made or expect this change.`
  );
}

/** What kind of item a changed name is, in words. */
export function repoConfigItemKind(item: string): string {
  if (item.startsWith('hooks/')) return 'hook script';
  if (item === 'info/attributes') return 'attributes file (can name filters and diff or merge drivers)';
  return 'git setting';
}

const listCss = css({
  margin: '0',
  paddingLeft: '20px',
  display: 'grid',
  gap: '2px',
  fontSize: t.textSm,
  '& code': { overflowWrap: 'anywhere' },
  '& .kind': { color: t.textMuted, marginLeft: t.s2 },
});

/** The changed items as a list: each name (verbatim, as code) and what kind of item it is. */
export function RepoConfigItems(handle: Handle<{ changed: readonly string[]; testId?: string }>) {
  return () => {
    const { changed, testId } = handle.props;
    if (changed.length === 0) return null;
    return (
      <ul mix={listCss} aria-label="Changed git configuration items" data-testid={testId}>
        {changed.map((c) => (
          <li key={c} data-item={c}>
            <code>{c}</code>
            <span class="kind">{repoConfigItemKind(c)}</span>
          </li>
        ))}
      </ul>
    );
  };
}

const gateCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& p': { margin: '0' },
  '& .what': { fontWeight: '500' },
  '& .root': { color: t.textMuted, fontSize: t.textSm, overflowWrap: 'anywhere' },
});

type Respond = (decision: string, data?: unknown) => Promise<void>;

/**
 * The run's approval for a changed repository configuration: the changed items, then Trust and continue (approved: the
 * core trusts exactly the state this request names) or Stop the run (rejected). Read-only (license) turns off trusting only.
 */
export function RepoConfigGatePanel(handle: Handle<{ payload: RepoConfigPayload; busy: boolean; readOnly: boolean; onRespond: Respond }>) {
  return () => {
    const { payload: p, busy, readOnly, onRespond } = handle.props;
    return (
      <div mix={gateCss} data-testid="repo-config-gate">
        <p class="what">{repoConfigText(p.changed)}</p>
        <RepoConfigItems changed={p.changed} testId="repo-config-gate-items" />
        {p.root ? (
          <p class="root">
            Repository: <code>{p.root}</code>
          </p>
        ) : null}
        <p>Trusting applies to exactly this state; if the configuration changes again, Drydock asks again.</p>
        <div mix={formActions}>
          <Button variant="primary" icon={ShieldCheck} disabled={busy || readOnly} mix={on('click', () => void onRespond('approved'))}>
            Trust and continue
          </Button>
          <Button variant="danger" icon={Octagon} disabled={busy} mix={on('click', () => void onRespond('rejected'))}>
            Stop the run
          </Button>
        </div>
      </div>
    );
  };
}

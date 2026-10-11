// The repository configuration banner (docs/security.md s3): above every screen of a project whose repository's own git
// configuration, hooks or info/attributes changed since a person last trusted them. Drydock refuses to run git there until a
// person reviews the change; the banner names the changed items (never their values) and offers "Trust this configuration",
// which trusts exactly the state shown (project.trustGitConfig with the fingerprint of that state). Nothing else trusts it:
// no default button, no shortcut, no automatic trust. If the configuration changed again in between, the core refuses the
// stale fingerprint and the banner shows the newer state to review again.
//
// The status is re-read on mount, when the stream reopens, on security.repo_config_* and approval/request events, and when
// the window gets focus again (a change made in a terminal publishes no event).

import { css, on, type Handle } from 'remix/component';
import { ShieldAlert, ShieldCheck } from 'lucide';
import { RepoConfigItems, repoConfigText } from '../screens/shared/repo-config.tsx';
import { starts } from '../state/live.ts';
import { action, query, services, watch } from '../state/services.tsx';
import { Button } from '../ui/button.tsx';
import { ErrorNote } from '../ui/feedback.tsx';
import { Icon } from '../ui/icon.tsx';
import { t, tone } from '../ui/tokens.ts';
import { ReadOnlyNote } from './license-banner.tsx';

const bannerCss = css({
  display: 'flex',
  alignItems: 'flex-start',
  gap: t.s3,
  padding: `${t.s3} ${t.s4}`,
  borderBottom: `1px solid color-mix(in srgb, ${t.warning} 45%, ${t.border})`,
  fontSize: t.textMd,
  color: t.text,
  '& > .icon': { display: 'inline-flex', marginTop: '2px', color: t.warning },
  '& > .body': { display: 'grid', gap: t.s2, minWidth: '0', flex: '1' },
  '& p': { margin: '0' },
  '& .actions': { display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2 },
});

/** Shown while the project's repository configuration is not the trusted one; renders nothing otherwise. */
export function RepoConfigBanner(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const mine = (e: { projectId?: string }) => !e.projectId || e.projectId === handle.props.projectId;
  const status = query(handle, {
    load: () => s.client.projectGitConfig(handle.props.projectId),
    when: (e) => mine(e) && starts(e, 'security.repo_config', 'approval.', 'workflow.request'),
    key: () => handle.props.projectId,
  });
  const act = action(handle);
  window.addEventListener('focus', () => status.reload(), { signal: handle.signal });

  /** Trust the state that was shown (its fingerprint), then read the status again either way. */
  const trust = async (fingerprint: string) => {
    if (act.busy) return;
    await act.run('trust', () => s.client.projectTrustGitConfig(handle.props.projectId, fingerprint));
    status.reload();
  };

  return () => {
    const st = status.data;
    if (!st || st.trusted) return null;
    const fingerprint = st.fingerprint; // the state on screen: trusting sends exactly this
    const readOnly = s.workspace.license.readOnly;
    return (
      <section mix={bannerCss} aria-label="Repository git configuration changed" data-testid="repo-config-banner" style={{ background: tone.warn.bg }}>
        <span class="icon" aria-hidden="true">
          <Icon icon={ShieldAlert} size={18} />
        </span>
        <div class="body">
          <p>
            <strong>Review the repository's git configuration.</strong> {repoConfigText(st.changed)}
          </p>
          <RepoConfigItems changed={st.changed} testId="repo-config-items" />
          <ErrorNote error={act.error} what="Not trusted" />
          {act.error ? <p>If the configuration changed again since it was shown, the items above are the newer state: review them again.</p> : null}
          <ReadOnlyNote />
          <div class="actions">
            <Button
              size="sm"
              variant="secondary"
              icon={ShieldCheck}
              disabled={!!act.busy || readOnly || !fingerprint}
              data-testid="repo-config-trust"
              mix={on('click', () => void trust(fingerprint))}
            >
              {act.busy === 'trust' ? 'Trusting...' : 'Trust this configuration'}
            </Button>
          </div>
        </div>
      </section>
    );
  };
}

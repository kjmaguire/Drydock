// The agent configuration banner (audit 2026-10-16, M3; docs/security.md s3 item 2b): above every screen of a project whose
// repository, or the worktree of an unfinished task, holds configuration that an agent CLI would act on and nobody trusted.
// Drydock does not start that CLI there, so a task of it fails to start: the banner names the CLI, the place and the files and
// offers Trust for exactly the state shown (project.trustAgentConfig with the adapter and the fingerprint of those files),
// like the git configuration banner next to it. Nothing trusts it by itself; the Agent configuration group of Project settings
// lists everything, trusted or not.
//
// The findings are re-read on mount, when the stream reopens, on task events (a worktree appears), on a refused start
// (agent.failed) and on security.agent_config_* events, and when the window gets focus again (files edited in a terminal publish
// no event).

import { css, type Handle } from 'remix/component';
import { ShieldAlert } from 'lucide';
import type { AgentConfigFinding } from '../api/types.ts';
import { routes } from '../routes.ts';
import { AgentConfigFindings } from '../screens/shared/agent-config.tsx';
import { trustKey, untrusted, untrustedSummary } from '../screens/shared/agent-config.ts';
import { starts } from '../state/live.ts';
import { action, query, services, watch } from '../state/services.tsx';
import { ErrorNote } from '../ui/feedback.tsx';
import { Icon } from '../ui/icon.tsx';
import { t, tone } from '../ui/tokens.ts';
import { ReadOnlyNote } from './license-banner.tsx';

/** How many findings the banner lists; the rest are in Project settings. */
export const BANNER_FINDINGS = 3;

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
});

/** Shown while a place holds agent CLI configuration nobody trusted; renders nothing otherwise. */
export function AgentConfigBanner(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const mine = (e: { projectId?: string }) => !e.projectId || e.projectId === handle.props.projectId;
  const status = query(handle, {
    load: () => s.client.projectAgentConfig(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => mine(e) && (starts(e, 'security.agent_config', 'task.') || e.type === 'agent.failed'),
  });
  const act = action(handle);
  window.addEventListener('focus', () => status.reload(), { signal: handle.signal });

  /** Trust the state that was shown (the adapter and the fingerprint), then show what the core answers with. */
  const trust = async (f: AgentConfigFinding) => {
    if (act.busy) return;
    const r = await act.run(trustKey(f), () => s.client.projectTrustAgentConfig(handle.props.projectId, f.adapter, f.fingerprint));
    if (r) status.set(r);
    else status.reload();
  };

  return () => {
    const list = status.data;
    const open = list ? untrusted(list) : [];
    if (!list || open.length === 0) return null;
    const shown = open.slice(0, BANNER_FINDINGS);
    return (
      <section mix={bannerCss} aria-label="Agent CLI configuration needs review" data-testid="agent-config-banner" style={{ background: tone.warn.bg }}>
        <span class="icon" aria-hidden="true">
          <Icon icon={ShieldAlert} size={18} />
        </span>
        <div class="body">
          <p>
            <strong>Review the configuration the repository brought for agent CLIs.</strong> {untrustedSummary(list)}. These files can run programs or send prompts
            elsewhere when the CLI starts, so Drydock does not start it there until you trust them.
          </p>
          <AgentConfigFindings findings={shown} busy={act.busy} readOnly={s.workspace.license.readOnly} onTrust={(f) => void trust(f)} label="Agent configuration to review" testId="agent-config-banner-findings" />
          {open.length > shown.length ? (
            <p>
              {open.length - shown.length} more: see <a href={routes.project.href()}>Project settings</a>, Agent configuration.
            </p>
          ) : null}
          <ErrorNote error={act.error} what="Not trusted" />
          {act.error ? <p>If the files changed since they were shown, the list above is the newer state: read it again.</p> : null}
          <ReadOnlyNote />
        </div>
      </section>
    );
  };
}

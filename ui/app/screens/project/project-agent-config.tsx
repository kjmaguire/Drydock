// Agent configuration (Project settings, audit 2026-10-16, M3): the files an agent CLI would act on in the project's repository
// and in the worktrees of its unfinished tasks (project.agentConfig). Gemini CLI loads its settings, hooks, MCP servers, commands
// and `.env` files from the folder it works in, OpenCode its plugins, and so on; a repository can bring any of them, and they can
// run programs or send prompts elsewhere when the CLI starts. Drydock does not start such a CLI where they are not trusted. This
// group says what was found and where, and trusts exactly what it showed (project.trustAgentConfig with the adapter and the
// fingerprint of those files; a human only). Trusting is per adapter and per state of the files: a file that changes asks again.

import type { Handle } from 'remix/component';
import type { AgentConfigFinding } from '../../api/types.ts';
import { starts } from '../../state/live.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { AgentConfigFindings } from '../shared/agent-config.tsx';
import { AGENT_CONFIG_WHY, trustKey, untrusted } from '../shared/agent-config.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { SettingsGroup, errorPad, noteRow } from './settings-kit.tsx';

export function AgentConfigPanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const mine = (e: { projectId?: string }) => !e.projectId || e.projectId === handle.props.projectId;
  const q = query(handle, {
    load: () => s.client.projectAgentConfig(handle.props.projectId),
    key: () => handle.props.projectId,
    // a worktree appears with a task, a refused start is an agent.failed, a trust elsewhere is a security event; files edited
    // in a terminal publish nothing, so the window getting focus reads again too
    when: (e) => mine(e) && (starts(e, 'security.agent_config', 'task.') || e.type === 'agent.failed'),
  });
  const act = action(handle);
  window.addEventListener('focus', () => q.reload(), { signal: handle.signal });

  /** Trusts what was on screen (its adapter and fingerprint), then shows the findings the core answers with. */
  const trust = async (f: AgentConfigFinding) => {
    if (act.busy) return;
    const r = await act.run(trustKey(f), () => s.client.projectTrustAgentConfig(handle.props.projectId, f.adapter, f.fingerprint));
    if (r) q.set(r);
    else q.reload(); // refused: the files changed since they were shown, so read them again
  };

  return () => {
    const list = q.data;
    const open = list ? untrusted(list).length : 0;
    return (
      <SettingsGroup title="Agent configuration" label="Agent configuration" testId="agent-config-panel" description={AGENT_CONFIG_WHY}>
        {!list ? (
          <div mix={errorPad}>
            <ErrorNote error={q.error} what="Could not read the agent configuration" />
            {q.error ? null : <Loading />}
          </div>
        ) : (
          <>
            {act.error ? (
              <div mix={errorPad}>
                <ErrorNote error={act.error} what="Not trusted" />
                <p mix={noteRow} style={{ border: '0', padding: '4px 0 0' }}>
                  If the files changed since they were shown, the list below is the newer state: read it again before you trust it.
                </p>
              </div>
            ) : null}
            {s.workspace.license.readOnly ? (
              <div mix={errorPad}>
                <ReadOnlyNote />
              </div>
            ) : null}
            {list.length === 0 ? (
              <p mix={noteRow} data-testid="agent-config-none">
                Nothing found: no agent CLI would act on configuration in the repository or in the worktrees of the unfinished tasks.
              </p>
            ) : (
              <>
                <p mix={noteRow} data-testid="agent-config-summary" style={{ border: '0' }}>
                  {open === 0 ? 'Everything found is trusted.' : `${open} of ${list.length} ${list.length === 1 ? 'place needs' : 'places need'} your decision before its CLI starts.`}
                </p>
                <AgentConfigFindings findings={list} busy={act.busy} readOnly={s.workspace.license.readOnly} onTrust={(f) => void trust(f)} label="Agent configuration found" />
              </>
            )}
          </>
        )}
      </SettingsGroup>
    );
  };
}

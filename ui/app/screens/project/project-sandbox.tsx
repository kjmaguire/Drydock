// The project's sandbox (internal/core/ext_sandbox.go, docs/sandbox.md): whether task work runs its commands in a container
// made for each task, what the definition on the default branch says, and a test that starts it once. Only the human switches
// it, and the core refuses anyone else.

import { css, on, type Handle } from 'remix/component';
import type { SandboxStatus, SandboxTestStep } from '../../api/extTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { SettingRow, SettingsGroup, ToggleRow, errorPad, noteRow } from './settings-kit.tsx';

const warnCss = css({ color: t.warning });
const valueCss = css({ fontSize: t.textMd, overflowWrap: 'anywhere' });
const preCss = css({ margin: '0', padding: t.s3, background: t.bgSurface, border: `1px solid ${t.border}`, borderRadius: t.rMd, fontFamily: t.mono, fontSize: t.textSm, whiteSpace: 'pre-wrap', overflowX: 'auto' });
const stepsCss = css({ margin: '0', padding: `0 ${t.s4} ${t.s3}`, listStyle: 'none', display: 'grid', gap: '4px', fontSize: t.textSm, '& .ok': { color: t.success }, '& .fail': { color: t.danger } });
const actionsCss = css({ display: 'flex', gap: t.s2, alignItems: 'center', padding: `${t.s3} ${t.s4}`, borderTop: `1px solid ${t.border}` });

/** What a valid definition says, in one line. */
export function sandboxSummary(st: SandboxStatus): string {
  const c = st.config;
  if (!c) return '';
  const services = Object.keys(c.services ?? {}).sort();
  const parts = [`image ${c.image}`];
  if (services.length > 0) parts.push(`services ${services.join(', ')}`);
  switch (c.network ?? 'allowlist') {
    case 'open':
      parts.push('open internet');
      break;
    case 'none':
      parts.push('no internet');
      break;
    default:
      parts.push(`network allowlist of ${(st.hosts ?? []).length} hosts`);
  }
  if ((c.setup ?? []).length > 0) parts.push(`setup: ${c.setup!.join(' && ')}`);
  return parts.join('; ');
}

/** Whether the switch may be turned on now (it can always be turned off). */
export function sandboxReady(st: SandboxStatus): boolean {
  return !!st.engine && st.found && !st.configError && !st.egressError;
}

export function SandboxPanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.sandboxStatus(handle.props.projectId), key: () => handle.props.projectId, when: (e) => e.type.startsWith('sandbox.') });
  const act = action(handle);
  let steps: SandboxTestStep[] | undefined;
  const toggle = async (enabled: boolean) => {
    if (await act.run('set', () => s.client.sandboxSet(handle.props.projectId, enabled))) q.reload();
  };
  const test = async () => {
    steps = undefined;
    const r = await act.run('test', () => s.client.sandboxTest(handle.props.projectId));
    if (r) {
      steps = r.steps;
      void handle.update();
    }
  };
  return () => {
    const st = q.data;
    return (
      <SettingsGroup
        title="Sandbox"
        label="Sandbox"
        testId="sandbox-panel"
        description="Run the agents' commands for task work in a container made for each task, instead of on this machine: installs, builds, tests, migrations, the app itself."
      >
        {!st ? (
          <div mix={errorPad}>
            <ErrorNote error={q.error} what="Could not read the sandbox status" />
            {q.error ? null : <Loading />}
          </div>
        ) : (
          <>
            {act.error ? (
              <div mix={errorPad}>
                <ErrorNote error={act.error} what={act.busy === 'test' ? 'The test did not finish' : 'Not changed'} />
              </div>
            ) : null}
            <SettingRow label="Container engine">
              {st.engine ? (
                <span mix={valueCss} data-testid="sandbox-engine">
                  {st.engine.client} {st.engine.version} ({st.engine.os}/{st.engine.arch})
                </span>
              ) : (
                <span mix={[valueCss, warnCss]} data-testid="sandbox-engine">
                  {st.engineError}. Install Docker Desktop (or another Docker-compatible engine), start it, and use Linux containers.
                </span>
              )}
            </SettingRow>
            <SettingRow label="Definition">
              <span mix={[valueCss, st.found && !st.configError ? undefined : warnCss]} data-testid="sandbox-definition">
                {st.found && !st.configError ? `${st.configPath} on ${st.branch}: ${sandboxSummary(st)}` : st.configError}
              </span>
            </SettingRow>
            {st.starter ? (
              <div mix={noteRow} data-testid="sandbox-starter">
                <p>
                  Commit this as <code>{st.configPath}</code> on <code>{st.branch}</code> to start from it. Drydock reads the definition from that branch only, so a
                  change an agent makes to it counts after you merged it.
                </p>
                <pre mix={preCss}>{st.starter}</pre>
              </div>
            ) : null}
            {st.egressError ? (
              <p mix={[noteRow, warnCss]} data-testid="sandbox-egress">
                {st.egressError}
              </p>
            ) : null}
            <ToggleRow
              label="Run the agents' commands in a container for each task"
              checked={st.switch.enabled}
              disabled={act.busy === 'set' || (!st.switch.enabled && !sandboxReady(st))}
              onChange={(v) => void toggle(v)}
            />
            {st.switch.enabled && st.switch.by ? (
              <p mix={noteRow} data-testid="sandbox-switch-note">
                Switched on by {st.switch.by.replace(/^human:/, '')}
                {st.switch.at ? ` on ${formatDateTime(st.switch.at)}` : ''}.
              </p>
            ) : null}
            <p mix={noteRow} data-testid="sandbox-explain">
              With it on, a Claude Code worker has no shell on this machine: it runs every command in the task's container, where only the task's working folder is
              mounted, the services listed in the definition are reachable by name, and the network reaches only the allowlist. The project's checks run there too,
              the e2e check and the preview do not run on this machine, and the container is removed when the task ends. Agents on other CLIs cannot work in a
              sandboxed project yet.
            </p>
            <div mix={actionsCss}>
              <Button size="sm" disabled={act.busy === 'test' || !sandboxReady(st)} mix={on('click', () => void test())}>
                {act.busy === 'test' ? 'Testing...' : 'Test the sandbox'}
              </Button>
              <span mix={css({ color: t.textMuted, fontSize: t.textSm })}>Starts it once without the setup commands, runs a command, and removes it.</span>
            </div>
            {steps ? (
              <ul mix={stepsCss} data-testid="sandbox-steps">
                {steps.map((x) => (
                  <li key={x.name} data-ok={String(x.ok)}>
                    <span class={x.ok ? 'ok' : 'fail'}>{x.ok ? 'OK' : 'Failed'}</span> {x.name} ({Math.round(x.durationMs / 100) / 10} s){x.detail ? `: ${x.detail}` : ''}
                  </li>
                ))}
              </ul>
            ) : null}
          </>
        )}
      </SettingsGroup>
    );
  };
}

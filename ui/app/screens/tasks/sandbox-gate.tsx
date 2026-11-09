// The question a task asks when its sandbox could not start (pipeline.sandboxTrouble, payload gate "sandbox"; effort review X38),
// answered on the task page and in the Inbox with buttons instead of a typed answer. The payload says why (reason):
// - unconfirmed: the definition changed since the person confirmed it. The changes are listed (text from the repository, shown as
//   text) and "Confirm this definition and try again" calls sandbox.trust with the fingerprint that was shown; the core then answers
//   every question of the project that waits for that confirmation, so the sentence beside the button says so.
// - engine, definition, start, agent: what fixes it in a sentence, then Try again (answered: the same step runs again).
// Every reason has Open sandbox settings (the project's General settings, where the Sandbox group is) and Stop (rejected).

import { css, on, type Handle } from 'remix/component';
import { Octagon, RefreshCw, Settings, ShieldCheck } from 'lucide';
import { isRecord } from '../../api/guards.ts';
import type { PendingRequest } from '../../api/types.ts';
import { visible } from '../../lib/visible.ts';
import { routes } from '../../routes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';

/** pipeline.sandboxTrouble's payload. An older core sends only step "sandbox" and the error: the reason is then unknown (''). */
export interface SandboxGatePayload {
  error: string;
  /** unconfirmed, engine, definition, start, agent; '' when the core did not say. */
  reason: string;
  /** The digest of the definition that was refused (unconfirmed only): Confirm confirms exactly it. */
  fingerprint?: string;
  changes: string[];
  projectId?: string;
}

export function sandboxGatePayload(p: PendingRequest): SandboxGatePayload | undefined {
  const v = p.spec.payload;
  if (p.spec.kind !== 'question' || !isRecord(v) || (v.gate !== 'sandbox' && v.step !== 'sandbox')) return undefined;
  const text = (x: unknown) => (typeof x === 'string' ? x : '');
  return {
    error: text(v.error),
    reason: text(v.reason),
    fingerprint: text(v.fingerprint) || undefined,
    changes: Array.isArray(v.changes) ? v.changes.filter((c): c is string => typeof c === 'string') : [],
    projectId: text(v.projectId) || undefined,
  };
}

/** What fixes the sandbox, in words, by the reason the core gave. */
export const SANDBOX_FIX: Record<string, string> = {
  unconfirmed: 'The sandbox definition changed since you confirmed it, so it does not run until you confirm it again. What changed:',
  engine: 'No container engine answers. Start Docker Desktop (the sandbox settings check it), then try again.',
  definition: 'The sandbox definition is missing or not valid. Fix it on the default branch (the sandbox settings show what is wrong), then try again.',
  start: 'The container engine answered, and starting the sandbox failed. Fix the cause, then try again: only this step runs again.',
  agent: "This step's agent cannot run in the sandbox. Assign another agent, or change where it runs, then try again.",
};

const gateCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& p': { margin: '0' },
  '& .changes': { margin: '0', paddingLeft: '20px', fontSize: t.textSm, color: t.textMuted, overflowWrap: 'anywhere' },
  '& .note': { color: t.textMuted, fontSize: t.textSm },
});

type Respond = (decision: string, data?: unknown) => Promise<void>;

export function SandboxGatePanel(handle: Handle<{ payload: SandboxGatePayload; busy: boolean; readOnly: boolean; onRespond: Respond; onDone?: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  return () => {
    const { payload: p, busy, readOnly, onRespond, onDone } = handle.props;
    const confirm = p.reason === 'unconfirmed' && !!p.fingerprint && !!p.projectId;
    const off = busy || readOnly || !!act.busy;
    return (
      <div mix={gateCss} data-testid="sandbox-gate" data-reason={p.reason}>
        <p data-testid="sandbox-gate-fix">{SANDBOX_FIX[p.reason] ?? 'Fix the cause (is the container engine running? is the sandbox definition right?), then try again.'}</p>
        {p.reason === 'unconfirmed' && p.changes.length > 0 ? (
          <ul class="changes" aria-label="What changed in the sandbox definition">
            {p.changes.map((c, i) => (
              <li key={`${i}:${c}`}>{visible(c)}</li>
            ))}
          </ul>
        ) : null}
        <ErrorNote error={act.error} what="Could not confirm the definition" />
        <div mix={formActions}>
          {confirm ? (
            <Button
              variant="primary"
              icon={ShieldCheck}
              disabled={off}
              data-testid="sandbox-gate-confirm"
              mix={on('click', async () => {
                const r = await act.run('confirm', () => s.client.sandboxTrust(p.projectId!, p.fingerprint!));
                if (r) onDone?.();
              })}
            >
              {act.busy === 'confirm' ? 'Confirming...' : 'Confirm this definition and try again'}
            </Button>
          ) : null}
          <Button variant={confirm ? 'secondary' : 'primary'} icon={RefreshCw} disabled={off} data-testid="sandbox-gate-retry" mix={on('click', () => void onRespond('answered', {}))}>
            Try again
          </Button>
          <a
            href={routes.project.href()}
            mix={[buttonStyle('secondary'), on('click', () => (p.projectId && p.projectId !== s.workspace.projectId ? s.workspace.selectProject(p.projectId) : undefined))]}
            data-testid="sandbox-gate-settings"
          >
            <Icon icon={Settings} size={14} />
            Open sandbox settings
          </a>
          <Button variant="danger" icon={Octagon} disabled={busy || !!act.busy} mix={on('click', () => void onRespond('rejected'))}>
            Stop
          </Button>
        </div>
        {confirm ? (
          <p class="note" data-testid="sandbox-gate-note">
            Confirming is for the whole project: every task of it that waits for this confirmation goes on.
          </p>
        ) : null}
      </div>
    );
  };
}

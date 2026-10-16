// A worker's permission request (docs/security.md s4 and s5). A Claude worker outside a sandbox asks Drydock before it uses a
// tool its rules do not cover; what Drydock's analyzer neither allows nor refuses waits for a person here, on the task page,
// while the worker's turn waits. The core lists it among the run's pending requests with gate "permission_prompt": the tool,
// its input as stored for display (secrets redacted), the approval kind, why it was asked and when it expires. Allowing covers
// exactly this tool with exactly this input, once; the agent can never answer it. The input is agent output: shown as text.

import { css, on, type Handle } from 'remix/component';
import { Check, X } from 'lucide';
import type { PendingRequest } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { visible } from '../../lib/visible.ts';
import { Button } from '../../ui/button.tsx';
import { formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { ShownNotes, Targets } from './approval-text.tsx';

export const PERMISSION_GATE = 'permission_prompt';

/** The payload of a permission request (internal/core/ext_permprompt.go permissionPending). */
export interface PermissionPayload {
  gate: 'permission_prompt';
  /** The CLI's tool name: Bash, Edit, WebFetch, mcp__server__tool. */
  tool: string;
  /** The command of a shell tool, else the JSON input with its short fields (where it goes) first; secrets redacted. */
  input: string;
  /** The paths and addresses the call names, each whole and in the order the call gave them. */
  targets: string[];
  /** A long value in the input is cut in `input`: Allow once still covers all of it, including what is not shown. */
  shortened: boolean;
  /** The approval kind the analyzer named (shell_destructive, fs_outside_worktree, agent_tool, ...). */
  kind: string;
  reason: string;
  agentId?: string;
  /** Unix milliseconds; nobody answering by then refuses the call. */
  expiresAt?: number;
}

export function permissionPayload(p: PendingRequest): PermissionPayload | undefined {
  const v = p.spec.payload as Partial<PermissionPayload> | null | undefined;
  if (!v || v.gate !== PERMISSION_GATE) return undefined;
  const str = (x: unknown) => (typeof x === 'string' ? x : '');
  return {
    gate: PERMISSION_GATE,
    tool: str(v.tool) || 'unknown tool',
    input: str(v.input),
    targets: Array.isArray(v.targets) ? v.targets.filter((x): x is string => typeof x === 'string' && x !== '') : [],
    shortened: v.shortened === true,
    kind: str(v.kind),
    reason: str(v.reason),
    agentId: str(v.agentId) || undefined,
    expiresAt: typeof v.expiresAt === 'number' && v.expiresAt > 0 ? v.expiresAt : undefined,
  };
}

/** Whether the input is a shell command (shown as one line of code) rather than JSON. */
export function isShellTool(tool: string): boolean {
  return tool === 'Bash' || tool === 'PowerShell';
}

/** The input as a person reads it: a shell command as it is, JSON indented when it parses. */
export function displayInput(p: PermissionPayload): string {
  if (isShellTool(p.tool)) return p.input;
  try {
    return JSON.stringify(JSON.parse(p.input), null, 2);
  } catch {
    return p.input; // a shortened input is not JSON any more: shown as stored
  }
}

const panelCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& p': { margin: '0' },
  '& dl': { display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: `4px ${t.s3}`, margin: '0', fontSize: t.textSm },
  '& dt': { color: t.textMuted },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere' },
  '& pre': {
    margin: '0',
    padding: `${t.s2} ${t.s3}`,
    borderRadius: t.rMd,
    border: `1px solid ${t.border}`,
    background: t.bgApp,
    fontFamily: t.mono,
    fontSize: t.textSm,
    maxHeight: '320px',
    overflow: 'auto',
    whiteSpace: 'pre-wrap',
    overflowWrap: 'anywhere',
  },
});

type Respond = (decision: string, data?: unknown) => Promise<void>;

/**
 * The tool, its exact input, why Drydock asks and when the request expires, then Allow once (approved) or Refuse (rejected).
 * Read-only (license) turns off allowing only.
 */
export function PermissionPromptPanel(handle: Handle<{ payload: PermissionPayload; busy: boolean; readOnly: boolean; onRespond: Respond }>) {
  return () => {
    const { payload: p, busy, readOnly, onRespond } = handle.props;
    return (
      <div mix={panelCss} data-testid="permission-prompt" data-tool={p.tool}>
        <p>
          The worker asks to use <code>{p.tool}</code>, which its rules do not allow. Its turn waits for your answer. Allowing covers exactly this
          call, once.
        </p>
        <Targets targets={p.targets} testId="permission-prompt-targets" />
        <ShownNotes shortened={p.shortened} texts={[p.input, ...p.targets]} testId="permission-prompt" />
        <pre aria-label={`${p.tool} input`} data-testid="permission-prompt-input">
          {visible(displayInput(p))}
        </pre>
        <dl>
          {p.kind ? (
            <>
              <dt>Kind</dt>
              <dd>
                <code>{p.kind}</code>
              </dd>
            </>
          ) : null}
          {p.reason ? (
            <>
              <dt>Why Drydock asks</dt>
              <dd data-testid="permission-prompt-reason">{p.reason}</dd>
            </>
          ) : null}
          {p.expiresAt ? (
            <>
              <dt>Refused if unanswered by</dt>
              <dd>{formatDateTime(p.expiresAt)}</dd>
            </>
          ) : null}
        </dl>
        <div mix={formActions}>
          <Button variant="primary" icon={Check} disabled={busy || readOnly} mix={on('click', () => void onRespond('approved'))}>
            Allow once
          </Button>
          <Button variant="danger" icon={X} disabled={busy} mix={on('click', () => void onRespond('rejected'))}>
            Refuse
          </Button>
        </div>
      </div>
    );
  };
}

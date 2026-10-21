// The confirmation a permission mode that skips approvals needs, and the badge that keeps saying so (audit 2026-10-16, M1;
// docs/security.md s4). bypassPermissions, auto and dontAsk take the approval step out of an agent's work, so the core gives an
// agent, a template or a crew member such a mode only when the request carries the person's explicit confirmation
// (`confirmSkipsApprovals`). Every form that lets a person choose a permission mode shows <SkipApprovals> under its select:
//
// - while the mode is one of the three, a warning in plain words and a box "I understand this agent will act without asking me
//   first"; the form keeps Save, Add or Apply off until the box is ticked and sends the flag only then (lib/permission.ts);
// - the box belongs to the mode it was ticked for, so choosing another such mode asks again;
// - if a request still comes back with the core's "needs your confirmation" refusal (a form state older than the choice), the
//   refusal is shown next to the box, where it can be answered, not as a general failure.
//
// <SkipsBadge> is the persistent red "skips approvals" on an agent, template or crew member that has such a mode.

import { css, on, type Handle } from 'remix/component';
import { ShieldAlert } from 'lucide';
import { SKIP_CONFIRM_LABEL, skipsApprovals } from '../../lib/permission.ts';
import { Badge } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';

const boxCss = css({
  display: 'grid',
  gap: t.s2,
  gridColumn: '1 / -1',
  minWidth: '0',
  padding: `${t.s3} ${t.s3}`,
  borderRadius: t.rMd,
  border: `1px solid color-mix(in srgb, ${t.danger} 35%, transparent)`,
  background: t.dangerSoft,
  color: t.text,
  fontSize: t.textMd,
  '& p': { margin: '0', overflowWrap: 'anywhere' },
  '& .warn': { display: 'flex', alignItems: 'flex-start', gap: t.s2 },
  '& .warn > svg': { flex: 'none', marginTop: '2px', color: t.danger },
  '& label': { display: 'flex', alignItems: 'flex-start', gap: t.s2, cursor: 'pointer', fontWeight: '500' },
  '& label > input': { flex: 'none', marginTop: '3px' },
  '& .err': { color: t.danger, fontSize: t.textSm },
});

/**
 * The warning and the box for a form whose permission mode is `mode`. `confirmed` is the mode the box is ticked for ('' when it
 * is not ticked); `onConfirm` gets that mode when the box is ticked and '' when it is cleared. `who` says what acts without
 * asking ("this agent", "agents made from this template"); `error` is the core's refusal, shown next to the box. Renders nothing
 * for a mode that keeps approvals, unless there is a refusal to show.
 */
export function SkipApprovals(handle: Handle<{ mode: string; confirmed: string; onConfirm: (mode: string) => void; who?: string; error?: Error; disabled?: boolean }>) {
  const id = `skip-${handle.id}`;
  return () => {
    const { mode, confirmed, onConfirm, who = 'this agent', error, disabled } = handle.props;
    const skip = skipsApprovals(mode);
    if (!skip && !error) return null;
    return (
      <div mix={boxCss} role="group" aria-label="Acting without asking" data-testid="skip-approvals" data-mode={mode}>
        {skip ? (
          <>
            <p class="warn" data-testid="skip-approvals-warning">
              <Icon icon={ShieldAlert} size={16} />
              <span>
                <strong>This mode skips approvals.</strong> The permission mode "{mode}" lets {who} act without asking you first. Nothing keeps it inside its working
                folder except its own behavior. Drydock's review of the finished work and your approval of a merge still apply.
              </span>
            </p>
            <label for={id}>
              <input
                id={id}
                type="checkbox"
                checked={confirmed === mode}
                disabled={disabled}
                aria-invalid={error ? 'true' : undefined}
                aria-describedby={error ? `${id}-error` : undefined}
                mix={on('change', (e) => onConfirm(e.currentTarget.checked ? mode : ''))}
              />
              <span>{SKIP_CONFIRM_LABEL}</span>
            </label>
          </>
        ) : null}
        {error ? (
          <p class="err" id={`${id}-error`} role="alert" data-testid="skip-approvals-error">
            {error.message}
          </p>
        ) : null}
      </div>
    );
  };
}

// the badge keeps its own width in a grid (a card's text column) instead of stretching across it
const badgeBox = css({ display: 'inline-flex', justifySelf: 'start', maxWidth: '100%' });

/** The persistent red "skips approvals" for an agent, template or crew member whose permission mode is one of the three. */
export function SkipsBadge(handle: Handle<{ mode: string | undefined }>) {
  return () => {
    const mode = handle.props.mode;
    if (!skipsApprovals(mode)) return null;
    return (
      <span mix={badgeBox}>
        <Badge tone="err" title={`Its permission mode (${mode}) lets it act without asking you first`} testId="skips-approvals-badge">
          <Icon icon={ShieldAlert} size={11} />
          skips approvals
        </Badge>
      </span>
    );
  };
}

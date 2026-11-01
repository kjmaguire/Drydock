// A segmented control: a row of choices, one of them chosen (low | medium | high). It is a radio group (role radiogroup, each
// choice a radio with aria-checked), so a screen reader says "high, radio button, checked, 3 of 4"; the arrow keys move the
// choice, as in a native radio group, and only the chosen one is in the Tab order.

import { css, on, ref, type Handle } from 'remix/component';
import { t } from './tokens.ts';

export interface Segment {
  value: string;
  label: string;
  /** A tooltip: what the choice means. */
  hint?: string;
}

const groupCss = css({
  display: 'inline-flex',
  flexWrap: 'wrap',
  maxWidth: '100%',
  padding: '2px',
  gap: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  '& button': {
    height: '28px',
    minHeight: '28px',
    padding: '0 10px',
    border: '0',
    borderRadius: t.rSm,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    fontWeight: '500',
    whiteSpace: 'nowrap',
  },
  '& button:hover:not(:disabled)': { color: t.text, background: t.bgHover },
  '& button[aria-checked="true"]': {
    color: t.text,
    background: t.bgActive,
    boxShadow: t.shadowSm,
  },
  '& button:focus-visible': { boxShadow: t.focus, outline: 'none' },
});

export function Segmented(
  handle: Handle<{
    label: string;
    value: string;
    options: readonly Segment[];
    onChange: (v: string) => void;
    disabled?: boolean;
    testId?: string;
  }>,
) {
  let group: HTMLElement | undefined;
  const move = (dir: number) => {
    const { options, value, onChange } = handle.props;
    if (options.length === 0) return;
    const i = options.findIndex((o) => o.value === value);
    const next = options[(Math.max(i, 0) + dir + options.length) % options.length]!;
    onChange(next.value);
    handle.queueTask(() => group?.querySelector<HTMLButtonElement>('[aria-checked="true"]')?.focus());
  };
  return () => {
    const { label, value, options, onChange, disabled, testId } = handle.props;
    const chosen = options.some((o) => o.value === value);
    return (
      <div
        role="radiogroup"
        aria-label={label}
        data-testid={testId}
        data-value={value}
        mix={[
          groupCss,
          ref((n) => (group = n as HTMLElement)),
          on('keydown', (e) => {
            if (disabled) return;
            if (e.key === 'ArrowRight' || e.key === 'ArrowDown') (e.preventDefault(), move(1));
            else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') (e.preventDefault(), move(-1));
          }),
        ]}
      >
        {options.map((o, i) => {
          const on_ = o.value === value;
          return (
            <button
              key={o.value}
              type="button"
              role="radio"
              aria-checked={on_ ? 'true' : 'false'}
              tabIndex={on_ || (!chosen && i === 0) ? 0 : -1}
              title={o.hint}
              disabled={disabled}
              data-value={o.value}
              mix={on('click', () => onChange(o.value))}
            >
              {o.label}
            </button>
          );
        })}
      </div>
    );
  };
}

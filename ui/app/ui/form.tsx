import { css, on, type Handle, type RemixNode } from 'remix/component';
import * as toggle from '@remix-run/ui/toggle';
import { t } from './tokens.ts';

const fieldCss = css({ display: 'grid', gap: '6px', minWidth: '0' });
const labelCss = css({ fontSize: t.textSm, fontWeight: '500', color: t.text });
const hintCss = css({ fontSize: t.textSm, color: t.textMuted, margin: '0' });
const errorCss = css({ fontSize: t.textSm, color: t.danger, margin: '0' });

/**
 * A labelled form control. The label is tied to the control by id, so its accessible name is exactly the label text (a
 * select's options never leak into it). children receives the id to put on the control.
 */
export function Field(handle: Handle<{ label: RemixNode; hint?: RemixNode; error?: string; children: (id: string) => RemixNode }>) {
  const id = `f-${handle.id}`;
  return () => {
    const { label, hint, error, children } = handle.props;
    return (
      <div mix={fieldCss}>
        <label for={id} mix={labelCss}>
          {label}
        </label>
        {children(id)}
        {hint ? <p mix={hintCss}>{hint}</p> : null}
        {error ? (
          <p mix={errorCss} role="alert">
            {error}
          </p>
        ) : null}
      </div>
    );
  };
}

/** Grid for forms: fields flow in columns that never get narrower than 220px. */
export const formGrid = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
  gap: `${t.s4} ${t.s4}`,
  alignItems: 'start',
});

/** A single column form. */
export const formStack = css({ display: 'grid', gap: t.s4, maxWidth: '640px' });

/** The row of buttons at the end of a form. */
export const formActions = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: t.s2,
  gridColumn: '1 / -1',
});

const checkRow = css({
  display: 'flex',
  alignItems: 'flex-start',
  gap: t.s2,
  fontSize: t.textMd,
  cursor: 'pointer',
  '& > input': { marginTop: '2px' },
  '& small': { display: 'block', color: t.textMuted, fontSize: t.textSm },
});

/** A checkbox with its label (and optional description) to the right. */
export function Checkbox(
  handle: Handle<{ label: RemixNode; description?: RemixNode; checked: boolean; disabled?: boolean; name?: string; onChange: (checked: boolean) => void }>,
) {
  return () => {
    const { label, description, checked, disabled, name, onChange } = handle.props;
    return (
      <label mix={checkRow}>
        <input
          type="checkbox"
          name={name}
          checked={checked}
          disabled={disabled}
          mix={on('change', (e) => onChange(e.currentTarget.checked))}
        />
        <span>
          {label}
          {description ? <small>{description}</small> : null}
        </span>
      </label>
    );
  };
}


const switchCss = css({
  position: 'relative',
  display: 'inline-block',
  flex: 'none',
  width: '30px',
  height: '18px',
  padding: '0',
  border: '0',
  borderRadius: t.rFull,
  background: t.borderStrong,
  transition: `background-color ${t.fast} ${t.ease}`,
  cursor: 'pointer',
  '&::after': {
    content: '""',
    position: 'absolute',
    top: '2px',
    left: '2px',
    width: '14px',
    height: '14px',
    borderRadius: '50%',
    background: t.onAccent,
    boxShadow: t.shadowSm,
    transition: `transform ${t.fast} ${t.ease}`,
  },
  '&[data-state="checked"]': { background: t.accent },
  '&[data-state="checked"]::after': { transform: 'translateX(12px)' },
  '&:hover': { background: t.textFaint },
  '&[data-state="checked"]:hover': { background: t.accentHover },
  '&:disabled': { opacity: '0.5', cursor: 'not-allowed' },
});

const switchRow = css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: t.s3, fontSize: t.textMd });

/** An on/off switch (role="switch") with its label. */
export function Switch(handle: Handle<{ label: RemixNode; checked: boolean; disabled?: boolean; onChange: (checked: boolean) => void }>) {
  const id = `sw-${handle.id}`;
  return () => {
    const { label, checked, disabled, onChange } = handle.props;
    return (
      <div mix={switchRow}>
        <label for={id}>{label}</label>
        <button id={id} mix={[switchCss, toggle.control({ checked, disabled, onCheckedChange: onChange })]} />
      </div>
    );
  };
}

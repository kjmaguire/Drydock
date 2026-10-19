import { css, on, type Handle, type RemixNode } from 'remix/component';
import * as toggle from '@remix-run/ui/toggle';
import { t } from './tokens.ts';

const fieldCss = css({ display: 'grid', gap: '6px', minWidth: '0' });
const labelCss = css({ fontSize: t.textSm, fontWeight: '500', color: t.text });
const hintCss = css({ fontSize: t.textSm, color: t.textMuted, margin: '0' });
/** The text of a field's error. A Field draws its own; a control that is not in a Field (a row of a list) uses it for the error it shows. */
export const errorCss = css({ fontSize: t.textSm, color: t.danger, margin: '0' });

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
          <p mix={errorCss} id={`${id}-error`} role="alert">
            {error}
          </p>
        ) : null}
      </div>
    );
  };
}

/**
 * The attributes that tie a control to the error its Field shows under it: the control is marked invalid and the error is its
 * description. Spread them on the control with the id the Field gave it: `<input id={id} {...errorAttrs(id, error)} />`.
 */
export function errorAttrs(id: string, error: string | undefined): { 'aria-invalid'?: 'true'; 'aria-describedby'?: string } {
  return error ? { 'aria-invalid': 'true', 'aria-describedby': `${id}-error` } : {};
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
  // the whole row is the target (28px high at least); the box itself is 18px, not the browser's 13
  minHeight: '28px',
  padding: '3px 0',
  '& > input': { flex: 'none', width: '18px', height: '18px', margin: '1px 0 0', cursor: 'pointer' },
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
  width: '40px',
  height: '24px',
  padding: '0',
  border: '0',
  borderRadius: t.rFull,
  background: t.borderStrong,
  transition: `background-color ${t.fast} ${t.ease}`,
  cursor: 'pointer',
  '&::after': {
    content: '""',
    position: 'absolute',
    top: '3px',
    left: '3px',
    width: '18px',
    height: '18px',
    borderRadius: '50%',
    background: t.onAccent,
    boxShadow: t.shadowSm,
    transition: `transform ${t.fast} ${t.ease}`,
  },
  '&[data-state="checked"]': { background: t.accent },
  '&[data-state="checked"]::after': { transform: 'translateX(16px)' },
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

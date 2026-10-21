// A Linear-style settings layout for the project screens: titled groups of rows, each row a label and description on the
// left and its control on the right, and a footer with the group's Save button.

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { t } from '../../ui/tokens.ts';

export const groupCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& > header h2': { fontSize: t.textLg, fontWeight: '600', margin: '0' },
  '& > header p': { margin: '4px 0 0', color: t.textMuted, fontSize: t.textSm, maxWidth: '620px' },
});

export const panelCss = css({
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  minWidth: '0',
});

/** A titled group of settings. `label` names the region (an aria-label) when it should differ from nothing. */
export function SettingsGroup(handle: Handle<{ title: string; description?: RemixNode; label?: string; testId?: string; children?: RemixNode }>) {
  return () => {
    const { title, description, label, testId, children } = handle.props;
    return (
      <section mix={groupCss} aria-label={label} data-testid={testId}>
        <header>
          <h2>{title}</h2>
          {description ? <p>{description}</p> : null}
        </header>
        <div mix={panelCss}>{children}</div>
      </section>
    );
  };
}

export const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: t.s4,
  padding: `${t.s3} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  minWidth: '0',
  '&:first-child': { borderTop: '0' },
  '& .text': { display: 'grid', gap: '2px', minWidth: '0', flex: '1' },
  '& .text label, & .text .name': { display: 'flex', alignItems: 'center', minHeight: '28px', fontSize: t.textMd, fontWeight: '500', color: t.text },
  '& .text p': { margin: '0', fontSize: t.textSm, color: t.textMuted },
  '& .control': { flex: 'none', display: 'flex', alignItems: 'center', gap: t.s2, maxWidth: '60%', minWidth: '0' },
  '& .control select': { minWidth: '220px', maxWidth: '100%' },
  '@media (max-width: 640px)': { flexDirection: 'column', alignItems: 'stretch', '& .control': { maxWidth: 'none' } },
});

/** A row whose control has its own label (pass `for` to tie the row label to the control). */
export function SettingRow(handle: Handle<{ label: RemixNode; for?: string; description?: RemixNode; children?: RemixNode }>) {
  return () => {
    const { label, for: htmlFor, description, children } = handle.props;
    return (
      <div mix={rowCss}>
        <div class="text">
          {htmlFor ? <label for={htmlFor}>{label}</label> : <span class="name">{label}</span>}
          {description ? <p>{description}</p> : null}
        </div>
        <div class="control">{children}</div>
      </div>
    );
  };
}

const switchCss = css({
  appearance: 'none',
  position: 'relative',
  flex: 'none',
  width: '40px',
  height: '24px',
  margin: '0',
  borderRadius: t.rFull,
  background: t.borderStrong,
  cursor: 'pointer',
  transition: `background-color ${t.fast} ${t.ease}`,
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
  '&:checked': { background: t.accent },
  '&:checked::after': { transform: 'translateX(16px)' },
  '&:focus-visible': { outline: 'none', boxShadow: t.focus },
  '&:disabled': { opacity: '0.45', cursor: 'not-allowed' },
});

/**
 * An on/off setting: a native checkbox drawn as a switch (so it stays a checkbox for assistive technology and tests), its
 * label (the accessible name) and a description.
 */
export function ToggleRow(handle: Handle<{ label: string; description?: RemixNode; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }>) {
  const id = `tg-${handle.id}`;
  return () => {
    const { label, description, checked, disabled, onChange } = handle.props;
    return (
      <div mix={rowCss}>
        <div class="text">
          <label for={id}>{label}</label>
          {description ? <p id={`${id}-d`}>{description}</p> : null}
        </div>
        <div class="control">
          <input
            id={id}
            type="checkbox"
            checked={checked}
            disabled={disabled}
            aria-describedby={description ? `${id}-d` : undefined}
            mix={[switchCss, on('change', (e) => onChange(e.currentTarget.checked))]}
          />
        </div>
      </div>
    );
  };
}

export const noteRow = css({
  padding: `${t.s2} ${t.s4} ${t.s3}`,
  margin: '0',
  fontSize: t.textSm,
  color: t.textMuted,
  borderTop: `1px solid ${t.border}`,
});

export const subhead = css({
  margin: '0',
  padding: `${t.s3} ${t.s4} ${t.s1}`,
  borderTop: `1px solid ${t.border}`,
  background: t.bgSidebar,
  color: t.textFaint,
  fontSize: t.textXs,
  fontWeight: '600',
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
});

export const footerCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  padding: `${t.s3} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  background: t.bgSidebar,
  '& .hint': { flex: '1', color: t.textMuted, fontSize: t.textSm, minWidth: '0' },
  '& [role="status"]': { color: t.success, fontSize: t.textSm, fontWeight: '500' },
});

export const errorPad = css({ padding: `${t.s3} ${t.s4} 0` });

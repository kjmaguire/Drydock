import { css, type Handle, type Props } from 'remix/component';
import { Icon, type IconNode } from './icon.tsx';
import { t } from './tokens.ts';

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'link';
export type ButtonSize = 'sm' | 'md';

const base = css({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: '6px',
  height: '30px',
  padding: '0 12px',
  border: '1px solid transparent',
  borderRadius: t.rMd,
  fontSize: t.textMd,
  fontWeight: '500',
  lineHeight: '1',
  whiteSpace: 'nowrap',
  userSelect: 'none',
  textDecoration: 'none',
  transition: `background-color ${t.fast} ${t.ease}, border-color ${t.fast} ${t.ease}, color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}`,
  '&:hover': { textDecoration: 'none' },
  '&:disabled, &[aria-disabled="true"]': { opacity: '0.5', pointerEvents: 'none' },
});

export const buttonVariant: Record<ButtonVariant, ReturnType<typeof css>> = {
  primary: css({
    background: t.accent,
    color: t.onAccent,
    boxShadow: `inset 0 1px 0 rgb(255 255 255 / 0.12), ${t.shadowSm}`,
    '&:hover': { background: t.accentHover, color: t.onAccent },
  }),
  secondary: css({
    background: t.bgRaised,
    color: t.text,
    borderColor: t.borderStrong,
    boxShadow: t.shadowSm,
    '&:hover': { background: t.bgOverlay, color: t.text },
  }),
  ghost: css({
    background: 'transparent',
    color: t.textMuted,
    '&:hover': { background: t.bgHover, color: t.text },
  }),
  danger: css({
    background: t.dangerSoft,
    color: t.danger,
    borderColor: 'transparent',
    '&:hover': { background: t.dangerSolid, color: t.onAccent },
  }),
  link: css({
    height: 'auto',
    padding: '0',
    background: 'transparent',
    color: t.accentText,
    fontWeight: '500',
    '&:hover': { textDecoration: 'underline' },
  }),
};

const small = css({ height: '26px', padding: '0 8px', fontSize: t.textSm, gap: '4px' });

/** The mixins that make any element (a link, a label) look like a button. */
export function buttonStyle(variant: ButtonVariant = 'secondary', size: ButtonSize = 'md') {
  return [base, buttonVariant[variant], size === 'sm' ? small : undefined];
}

type ButtonProps = Props<'button'> & { variant?: ButtonVariant; size?: ButtonSize; icon?: IconNode };

/** A native button. type defaults to "button" so nothing submits by accident. */
export function Button(handle: Handle<ButtonProps>) {
  return () => {
    const { variant = 'secondary', size = 'md', icon, children, mix, type, ...rest } = handle.props;
    return (
      <button {...rest} type={type ?? 'button'} mix={[...buttonStyle(variant, size), ...(mix ?? [])]}>
        {icon ? <Icon icon={icon} size={size === 'sm' ? 14 : 16} /> : null}
        {children}
      </button>
    );
  };
}

const iconOnly = css({
  display: 'inline-grid',
  placeItems: 'center',
  width: '28px',
  height: '28px',
  padding: '0',
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textMuted,
  transition: `background-color ${t.fast} ${t.ease}, color ${t.fast} ${t.ease}`,
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-pressed="true"], &[aria-expanded="true"]': { background: t.bgActive, color: t.text },
  '&:disabled': { opacity: '0.4', pointerEvents: 'none' },
});

/** An icon-only button; `label` is its accessible name and tooltip. */
export function IconButton(handle: Handle<Props<'button'> & { icon: IconNode; label: string; size?: number }>) {
  return () => {
    const { icon, label, size = 16, mix, type, ...rest } = handle.props;
    return (
      <button {...rest} type={type ?? 'button'} aria-label={label} title={rest.title ?? label} mix={[iconOnly, ...(mix ?? [])]}>
        <Icon icon={icon} size={size} />
      </button>
    );
  };
}

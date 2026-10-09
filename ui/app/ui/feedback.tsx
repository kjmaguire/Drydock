import { css, type Handle, type RemixNode } from 'remix/component';
import { CircleAlert, Info, TriangleAlert, CircleCheck } from 'lucide';
import { Icon } from './icon.tsx';
import { t, tone, type Tone } from './tokens.ts';

const errorCss = css({
  display: 'flex',
  gap: t.s2,
  alignItems: 'flex-start',
  padding: '8px 12px',
  borderRadius: t.rMd,
  background: t.dangerSoft,
  color: t.text,
  fontSize: t.textMd,
  border: `1px solid color-mix(in srgb, ${t.danger} 30%, transparent)`,
  '& svg': { color: t.danger, marginTop: '2px' },
  overflowWrap: 'anywhere',
});

/** An error, shown verbatim (role="alert"). Renders nothing without one. */
export function ErrorNote(handle: Handle<{ error: unknown; what?: string }>) {
  return () => {
    const { error, what } = handle.props;
    if (!error) return null;
    const msg = error instanceof Error ? error.message : String(error);
    return (
      <div mix={errorCss} role="alert">
        <Icon icon={CircleAlert} size={15} />
        <div>
          {what ? <strong>{what}: </strong> : null}
          {msg}
        </div>
      </div>
    );
  };
}

const noticeCss = css({
  display: 'flex',
  gap: t.s2,
  alignItems: 'flex-start',
  padding: '8px 12px',
  borderRadius: t.rMd,
  fontSize: t.textMd,
  color: t.text,
  '& > svg': { marginTop: '2px' },
});

const NOTICE_ICON = { info: Info, warn: TriangleAlert, ok: CircleCheck, err: CircleAlert, muted: Info, accent: Info };

/** A quiet callout. role defaults to "note"; pass role="status" for something that just happened. */
export function Notice(handle: Handle<{ tone?: Tone; role?: 'note' | 'status' | 'alert'; title?: RemixNode; children?: RemixNode; testId?: string }>) {
  return () => {
    const { tone: tn = 'info', role = 'note', title, children, testId } = handle.props;
    const c = tone[tn];
    return (
      <div mix={noticeCss} role={role} data-testid={testId} style={{ background: c.bg }}>
        <span style={{ color: c.fg, display: 'inline-flex' }}>
          <Icon icon={NOTICE_ICON[tn]} size={15} />
        </span>
        <div>
          {title ? <strong>{title} </strong> : null}
          {children}
        </div>
      </div>
    );
  };
}

const spin = css({
  display: 'inline-block',
  width: '14px',
  height: '14px',
  borderRadius: '50%',
  border: `2px solid ${t.borderStrong}`,
  borderTopColor: t.accent,
  animation: 'dd-spin 700ms linear infinite',
  verticalAlign: 'middle',
  '@keyframes dd-spin': { to: { transform: 'rotate(360deg)' } },
});

export function Spinner(handle: Handle<{ label?: string }>) {
  return () => <span mix={spin} role="status" aria-label={handle.props.label ?? 'Loading'} />;
}

const loadingCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, padding: t.s6, color: t.textMuted, fontSize: t.textMd });

/** "Loading..." with a spinner, for a region whose first read has not answered yet. */
export function Loading(handle: Handle<{ label?: string }>) {
  return () => (
    <p mix={loadingCss}>
      <span mix={spin} aria-hidden="true" />
      {handle.props.label ?? 'Loading...'}
    </p>
  );
}

const emptyCss = css({
  display: 'grid',
  justifyItems: 'center',
  gap: t.s2,
  padding: `${t.s8} ${t.s6}`,
  textAlign: 'center',
  color: t.textMuted,
  '& h2': { color: t.text, fontSize: t.textLg },
  '& p': { maxWidth: '440px' },
});

const emptyIcon = css({
  display: 'grid',
  placeItems: 'center',
  width: '44px',
  height: '44px',
  marginBottom: t.s1,
  borderRadius: t.rLg,
  background: t.bgActive,
  color: t.textMuted,
});

/** An empty state: what is missing and, in children, what to do about it. */
export function Empty(handle: Handle<{ title: string; icon?: import('./icon.tsx').IconNode; children?: RemixNode }>) {
  return () => {
    const { title, icon, children } = handle.props;
    return (
      <div mix={emptyCss}>
        {icon ? (
          <span mix={emptyIcon}>
            <Icon icon={icon} size={20} />
          </span>
        ) : null}
        <h2>{title}</h2>
        {children}
      </div>
    );
  };
}

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { CircleAlert, Info, TriangleAlert, CircleCheck } from 'lucide';
import { Button } from './button.tsx';
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

/**
 * Whether the shell has said, once, that Drydock's core is not answering (its banner, shell/core-banner.tsx, decision G4).
 * While it has, a read that failed for that same reason (a network error) says nothing more on its own: the banner says it once
 * with Try now, instead of every panel in a red note of its own.
 */
class CoreDown extends EventTarget {
  #down = false;
  get down(): boolean {
    return this.#down;
  }
  set(down: boolean): void {
    if (down === this.#down) return;
    this.#down = down;
    this.dispatchEvent(new Event('change'));
  }
}
export const coreDown = new CoreDown();

/** A call that never completed (the transport's RpcError kind "network"): the core did not answer at all. */
export function isNetworkError(error: unknown): boolean {
  return error instanceof Error && (error as { kind?: unknown }).kind === 'network';
}

/** What a failed call says: the core's own words, or, when it did not answer at all, that. */
export function errorText(error: unknown): string {
  if (isNetworkError(error)) return "Drydock's core did not answer.";
  return error instanceof Error ? error.message : String(error);
}

const errorBody = css({ flex: '1', minWidth: '0' });
const errorActions = css({ display: 'flex', gap: t.s2, flex: 'none', alignItems: 'center', marginTop: '-3px', marginBottom: '-3px' });

/**
 * An error (role="alert"). Renders nothing without one. `retry` (a read's reload) adds Retry, which reads again; `action` is
 * another button of the caller's. A network error says the core did not answer, and nothing while the shell's banner says so.
 */
export function ErrorNote(handle: Handle<{ error: unknown; what?: string; retry?: () => unknown; action?: RemixNode }>) {
  coreDown.addEventListener('change', () => void handle.update(), { signal: handle.signal });
  let retrying = false;
  const runRetry = async (retry: () => unknown) => {
    if (retrying) return;
    retrying = true;
    void handle.update();
    try {
      await retry();
    } catch {
      /* the read shows its own new error */
    } finally {
      retrying = false;
      if (!handle.signal.aborted) void handle.update();
    }
  };
  return () => {
    const { error, what, retry, action } = handle.props;
    if (!error) return null;
    if (coreDown.down && isNetworkError(error)) return null;
    return (
      <div mix={errorCss} role="alert">
        <Icon icon={CircleAlert} size={15} />
        <div mix={errorBody}>
          {what ? <strong>{what}: </strong> : null}
          {errorText(error)}
        </div>
        {retry || action ? (
          <div mix={errorActions}>
            {retry ? (
              <Button size="sm" variant="secondary" disabled={retrying} mix={on('click', () => void runRetry(retry))}>
                {retrying ? 'Retrying...' : 'Retry'}
              </Button>
            ) : null}
            {action}
          </div>
        ) : null}
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
    <p mix={loadingCss} role="status">
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

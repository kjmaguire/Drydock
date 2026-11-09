// When the core stops answering, the shell says so once (decision G4): a banner over the page after the stream has been
// broken for a few seconds, with Try now (the stream and the workspace's reads, at once instead of after the backoff) and, in
// the desktop window, Restart Drydock (shell.restart, asked twice: it ends every agent). While it shows, a read that failed
// for the same reason says nothing more in its own note (coreDown in ui/feedback.tsx). The "Cannot reach the Drydock core"
// page uses the same buttons (CoreActions).

import { css, on, type Handle } from 'remix/component';
import { PlugZap } from 'lucide';
import { services } from '../state/services.tsx';
import { Button } from '../ui/button.tsx';
import { coreDown } from '../ui/feedback.tsx';
import { Icon } from '../ui/icon.tsx';
import { t, tone } from '../ui/tokens.ts';

/** How long the stream is broken before the banner shows: a blip that heals by itself says nothing. */
export const CORE_DOWN_AFTER_MS = 3000;

const bannerCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: t.s2,
  minHeight: '36px',
  padding: `4px ${t.s4}`,
  fontSize: t.textSm,
  borderBottom: `1px solid ${t.border}`,
  '& > span': { flex: '1', minWidth: '200px' },
});
const actionsCss = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2 });

/** What the window says of a core it cannot reach, by how it reaches it: the desktop window holds the core, the dev transport an address. */
export function coreDownWords(transport: string): string {
  return transport === 'http'
    ? "Drydock's core is not answering at this address. It may have stopped: start it again, then Try now."
    : "Drydock's core is not answering. It keeps trying; agents that are still running carry on.";
}

/** Try now, and Restart Drydock asked twice when the window can restart it. */
export function CoreActions(handle: Handle<{ onTry?: () => void }>) {
  const s = services(handle);
  let confirming = false;
  let restarting = false;
  let error = '';
  const tryNow = () => {
    s.store.retryNow();
    for (const l of [s.workspace.projects, s.workspace.info, s.workspace.needs]) void l.reload();
    handle.props.onTry?.();
  };
  const restart = async () => {
    restarting = true;
    error = '';
    void handle.update();
    try {
      await s.client.shellRestart();
    } catch (e) {
      restarting = false;
      confirming = false;
      error = e instanceof Error ? e.message : String(e);
      void handle.update();
    }
  };
  return () => {
    const canRestart = s.workspace.info.data?.canRestart === true;
    return (
      <div mix={actionsCss}>
        {confirming ? (
          <>
            <span data-testid="restart-confirm">Restarting ends every running agent. Restart Drydock?</span>
            <Button size="sm" variant="danger" disabled={restarting} mix={on('click', () => void restart())}>
              {restarting ? 'Restarting...' : 'Restart Drydock'}
            </Button>
            <Button size="sm" variant="ghost" disabled={restarting} mix={on('click', () => ((confirming = false), void handle.update()))}>
              Cancel
            </Button>
          </>
        ) : (
          <>
            <Button size="sm" variant="secondary" mix={on('click', tryNow)}>
              Try now
            </Button>
            {canRestart ? (
              <Button size="sm" variant="ghost" mix={on('click', () => ((confirming = true), void handle.update()))}>
                Restart Drydock...
              </Button>
            ) : null}
          </>
        )}
        {error ? <span role="alert">Could not restart: {error}</span> : null}
      </div>
    );
  };
}

/** The banner: shown once the stream has been broken for `afterMs`; hidden again as soon as it opens. */
export function CoreBanner(handle: Handle<{ afterMs?: number; hidden?: boolean }>) {
  const s = services(handle);
  let shown = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const sync = () => {
    const down = s.store.getSnapshot().status === 'reconnecting';
    if (!down) {
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      if (shown) {
        shown = false;
        coreDown.set(false);
        void handle.update();
      }
      return;
    }
    if (shown || timer !== undefined) return;
    timer = setTimeout(() => {
      timer = undefined;
      if (s.store.getSnapshot().status !== 'reconnecting') return;
      shown = true;
      coreDown.set(true);
      void handle.update();
    }, handle.props.afterMs ?? CORE_DOWN_AFTER_MS);
  };
  const unsubscribe = s.store.subscribe(sync);
  handle.signal.addEventListener('abort', () => {
    unsubscribe();
    if (timer !== undefined) clearTimeout(timer);
    if (shown) coreDown.set(false);
  });
  sync();
  return () => {
    if (!shown || handle.props.hidden) return null;
    const c = tone.warn;
    return (
      <div mix={bannerCss} style={{ background: c.bg, color: t.text }} role="alert" data-testid="core-banner">
        <span style={{ color: c.fg, display: 'inline-flex', flex: 'none', minWidth: '0' }}>
          <Icon icon={PlugZap} size={15} />
        </span>
        <span>{coreDownWords(s.transportName)}</span>
        <CoreActions />
      </div>
    );
  };
}

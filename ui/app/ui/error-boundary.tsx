// A region error boundary (docs/ui.md 6.6, audit 2026-10-16 H11). Without one, a component that throws while it draws leaves
// the window as it was (an update) or blank (a first draw), and the only trace is a line in the console.
//
// What it can catch, and what it can not. The Remix runtime renders a component's children itself, after the parent's
// render function has returned, so a parent can not wrap them in try/catch. A boundary therefore guards the *components it is
// given*: every component element in its children (looking through host elements and fragments) is swapped for a guarded
// twin, which runs the component's setup and its render function inside try/catch. When one throws, the twin draws the error
// panel in its place - the message, the error's text, Try again and Reload - and the rest of the window carries on.
//   - A region is as wide as the components handed to it: the shell as a whole, the routed screen, the dock, the details
//     panel. A component *inside* a guarded one that throws in its own render is not caught here: the runtime reports it (it
//     keeps the old picture on an update, draws nothing on a first draw) and main.tsx shows the runtime failure bar.
//   - Errors thrown later by an event handler or a promise are not render errors and are not caught.
// Try again mounts the guarded components afresh (a new setup, new queries); a failed region also tries again by itself when
// `resetKey` changes (the route), but a healthy one is left alone, so its state survives the same navigation.

import { css, Fragment, Frame, on, type Handle, type RemixElement, type RemixNode } from 'remix/component';
import { TriangleAlert } from 'lucide';
import { errorText } from '../lib/failure.ts';
import { Button } from './button.tsx';
import { Icon } from './icon.tsx';
import { t } from './tokens.ts';

type Factory = (handle: Handle<any>) => () => RemixNode;

/** What the guarded components of one boundary share with it. */
interface Region {
  label: string;
  /** The guards that are showing the panel now. */
  failed: Set<object>;
  retry: () => void;
  reload: () => void;
  report: (error: unknown) => void;
}

const wrapCss = css({ boxSizing: 'border-box', minHeight: '0', overflow: 'auto', padding: t.s6 });
const cardCss = css({
  display: 'grid',
  gap: t.s3,
  maxWidth: '640px',
  margin: '0 auto',
  padding: t.s5,
  borderRadius: t.rLg,
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  '& h2': { display: 'flex', alignItems: 'center', gap: t.s2, margin: '0', fontSize: t.textLg },
  '& h2 svg': { color: t.danger, flex: 'none' },
  '& p': { margin: '0', color: t.textMuted },
  '& pre': { margin: '0', maxHeight: '220px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  '& .actions': { display: 'flex', flexWrap: 'wrap', gap: t.s2 },
});

/** The panel a failed region shows: what failed (the error's own text), and how to get on. */
function RegionError(handle: Handle<{ error: unknown; label: string; onRetry: () => void; onReload: () => void }>) {
  return () => {
    const { error, label, onRetry, onReload } = handle.props;
    return (
      <div mix={wrapCss}>
        <div mix={cardCss} role="alert" data-testid="region-error">
          <h2>
            <Icon icon={TriangleAlert} size={18} />
            Could not show {label}
          </h2>
          <p>Something went wrong while drawing it. The rest of Drydock is still running. Try again, or reload Drydock if it keeps happening.</p>
          <pre data-testid="region-error-text">{errorText(error)}</pre>
          <div class="actions">
            <Button variant="primary" mix={on('click', () => onRetry())}>
              Try again
            </Button>
            <Button variant="secondary" mix={on('click', () => onReload())}>
              Reload Drydock
            </Button>
          </div>
        </div>
      </div>
    );
  };
}

/** The same component with its setup and render inside try/catch, drawing the panel when either throws. */
function guardFor(Inner: Factory, region: Region): Factory {
  const Guarded: Factory = (handle) => {
    const me = {};
    let render: (() => RemixNode) | undefined;
    let failure: { error: unknown } | undefined;
    let reported: string | undefined;
    const fail = (error: unknown) => {
      failure = { error };
      region.failed.add(me);
      const text = errorText(error);
      if (reported !== text) {
        reported = text;
        console.error(`Drydock could not draw ${region.label}:`, error);
        region.report(error);
      }
    };
    handle.signal.addEventListener('abort', () => region.failed.delete(me), { once: true });
    try {
      const r = Inner(handle);
      if (typeof r !== 'function') throw new TypeError(`${Inner.name || 'A component'} did not return a render function`);
      render = r;
    } catch (e) {
      fail(e);
    }
    return () => {
      if (render) {
        try {
          const out = render();
          failure = undefined;
          region.failed.delete(me);
          return out;
        } catch (e) {
          fail(e);
        }
      }
      return <RegionError error={failure?.error} label={region.label} onRetry={region.retry} onReload={region.reload} />;
    };
  };
  Object.defineProperty(Guarded, 'name', { value: Inner.name || 'Guarded' });
  return Guarded;
}

const isElement = (node: unknown): node is RemixElement => typeof node === 'object' && node !== null && (node as RemixElement).$rmx === true;

/** `node` with the component elements in it (through host elements and fragments) swapped for their guarded twins. */
function protect(node: RemixNode, guard: (type: Factory) => Factory): RemixNode {
  if (Array.isArray(node)) return node.map((n) => protect(n, guard));
  if (!isElement(node)) return node;
  const { type, props } = node;
  if (typeof type === 'string' || type === Fragment) {
    return props.children === undefined ? node : { ...node, props: { ...props, children: protect(props.children, guard) } };
  }
  // a mixin placeholder and a Frame are the runtime's own: they are not ours to wrap
  if (type === Frame || '__rmxMixinElementType' in type) return node;
  return { ...node, type: guard(type as Factory) };
}

/**
 * Guards the components in its children (see the top of this file). `label` names the region in the panel ("this page", "the
 * dock"); `resetKey` makes a failed region try again when it changes; `reload` is what Reload Drydock does (the page is reloaded
 * by default); `onError` hears each new error.
 */
export function ErrorBoundary(handle: Handle<{ label?: string; resetKey?: unknown; reload?: () => void; onError?: (error: unknown) => void; children?: RemixNode }>) {
  let attempt = 0;
  let seenKey = handle.props.resetKey;
  const guards = new Map<Factory, Factory>();
  const region: Region = {
    label: 'this part of Drydock',
    failed: new Set(),
    retry: () => {
      attempt++;
      void handle.update();
    },
    reload: () => (handle.props.reload ?? (() => window.location.reload()))(),
    report: (error) => handle.props.onError?.(error),
  };
  const guard = (type: Factory): Factory => {
    let g = guards.get(type);
    if (!g) {
      g = guardFor(type, region);
      guards.set(type, g);
    }
    return g;
  };
  return () => {
    const { children, resetKey, label } = handle.props;
    region.label = label ?? 'this part of Drydock';
    if (resetKey !== seenKey) {
      seenKey = resetKey;
      if (region.failed.size > 0) attempt++; // a healthy region keeps its state across the navigation
    }
    // keyed inside an array: the runtime matches keys among siblings, not at the root of a component's content
    return [<Fragment key={attempt}>{protect(children, guard)}</Fragment>];
  };
}

// The overview tiles the Integrations screen introduced, shared by every screen that lists services, CLIs, providers or library
// entries: a grid of equal tiles, each with a logo (or an icon when the thing has no brand), a name, a line saying what it is and a
// line saying its state. Clicking a tile selects it; the screen shows the selected one's panel below the grid.

import { css, on, type Handle } from 'remix/component';
import type { IconNode } from 'lucide';
import { BrandLogo, brandKey } from './brand-logo.tsx';
import { Icon } from './icon.tsx';
import { t } from './tokens.ts';

export const tileGrid = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
});

export const tileCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  width: '100%',
  height: '100%',
  padding: `${t.s3} ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  color: t.text,
  textAlign: 'left',
  cursor: 'pointer',
  transition: `border-color ${t.fast} ${t.ease}, background-color ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong },
  '&[aria-pressed="true"]': { borderColor: t.accent, background: t.accentSoft },
  '&[data-dragging="true"]': { opacity: '0.55', borderStyle: 'dashed' },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& strong': { display: 'block', fontWeight: '600', overflowWrap: 'anywhere' },
  '& small': { display: 'block', color: t.textMuted, fontSize: t.textXs, overflowWrap: 'anywhere' },
  '& small[data-tone="ok"]': { color: t.success },
  '& small[data-tone="warn"]': { color: t.warning },
  '& small[data-tone="bad"]': { color: t.danger },
});

/** The line above an empty grid: an icon, a bold sentence and what to do next. */
export const emptyLine = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', margin: '0', color: t.textMuted, fontSize: t.textMd, '& strong': { color: t.text } });

const glyph = css({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flex: 'none',
  width: '32px',
  height: '32px',
  borderRadius: '7px',
  background: t.bgApp,
  color: t.textMuted,
  boxShadow: `inset 0 0 0 1px ${t.border}`,
});

export type TileTone = 'ok' | 'warn' | 'bad' | '';

/**
 * One tile. `logo` is a brand id (BrandLogo); a thing without a brand shows `icon` on a neutral square instead. `blurb` says what it
 * is, `state` what state it is in (with an optional tone). Extra data attributes go through `data` for tests and styling.
 * `onDragStart` makes the tile draggable (HTML5 drag and drop); `dragging` dims it while it is being dragged.
 */
export function Tile(
  handle: Handle<{
    title: string;
    logo?: string;
    icon?: IconNode;
    blurb?: string;
    state?: string;
    tone?: TileTone;
    selected?: boolean;
    onSelect?: () => void;
    testId?: string;
    data?: Record<string, string>;
    onDragStart?: (e: DragEvent) => void;
    onDragEnd?: (e: DragEvent) => void;
    dragging?: boolean;
  }>,
) {
  return () => {
    const { title, logo, icon, blurb, state, tone = '', selected = false, onSelect, testId, data = {}, onDragStart, onDragEnd, dragging } = handle.props;
    const attrs: Record<string, string> = {};
    for (const [k, v] of Object.entries(data)) attrs[`data-${k}`] = v;
    return (
      <li>
        <button
          type="button"
          mix={[
            tileCss,
            onSelect ? on('click', onSelect) : undefined,
            onDragStart ? on<HTMLButtonElement, 'dragstart'>('dragstart', onDragStart) : undefined,
            onDragEnd ? on<HTMLButtonElement, 'dragend'>('dragend', onDragEnd) : undefined,
          ]}
          aria-pressed={selected ? 'true' : 'false'}
          draggable={onDragStart ? 'true' : undefined}
          data-dragging={onDragStart ? String(!!dragging) : undefined}
          data-testid={testId}
          {...attrs}
        >
          {logo && brandKey(logo) ? (
            <BrandLogo id={logo} size={32} />
          ) : (
            <span mix={glyph} aria-hidden="true">
              {icon ? <Icon icon={icon} size={16} /> : null}
            </span>
          )}
          <span>
            <strong>{title}</strong>
            {blurb ? <small>{blurb}</small> : null}
            {state ? <small data-tone={tone || undefined}>{state}</small> : null}
          </span>
        </button>
      </li>
    );
  };
}

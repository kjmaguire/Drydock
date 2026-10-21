// Styles shared by the dock panels: an IDE-like frame (compact toolbar, side list, content pane) and a segmented control.

import { css } from 'remix/component';
import { t } from '../../ui/tokens.ts';

/** A panel that fills the dock (or, on a page, its container) and lays out toolbar + body. */
export const panel = css({
  display: 'flex',
  flexDirection: 'column',
  height: '100%',
  minHeight: '0',
  minWidth: '0',
  background: t.bgApp,
});

/** The compact toolbar at the top of a panel. */
export const toolbar = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: t.s2,
  minHeight: '36px',
  padding: `4px ${t.s2}`,
  borderBottom: `1px solid ${t.border}`,
  background: t.bgSurface,
  fontSize: t.textSm,
  flex: 'none',
  '& > .sep': { width: '1px', height: '16px', background: t.border },
});

export const toolbarNote = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  color: t.textMuted,
  fontSize: t.textSm,
  minWidth: '0',
  '& code': { fontFamily: t.mono, fontSize: t.textXs },
});

/** A segmented control: a group of pressable buttons (aria-pressed marks the chosen one). */
export const segmented = css({
  display: 'inline-flex',
  padding: '2px',
  gap: '2px',
  borderRadius: t.rMd,
  background: t.bgActive,
  '& > button': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '5px',
    minHeight: '28px',
    height: '28px',
    padding: '0 8px',
    border: '0',
    borderRadius: t.rSm,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    fontWeight: '500',
  },
  '& > button:hover': { color: t.text, background: 'transparent' },
  '& > button[aria-pressed="true"]': { background: t.bgRaised, color: t.text, boxShadow: t.shadowSm },
  '& > button:disabled': { opacity: '0.45', pointerEvents: 'none' },
});

/** Body: a side list and a content pane. */
export const split = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(180px, 260px) minmax(0, 1fr)',
  flex: '1',
  minHeight: '0',
});

/** The side list (files, logs). Items are buttons. */
export const sideList = css({
  listStyle: 'none',
  margin: '0',
  padding: `${t.s1} ${t.s2}`,
  overflowY: 'auto',
  borderRight: `1px solid ${t.border}`,
  background: t.bgSidebar,
  minHeight: '0',
  '& > li > button': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    width: '100%',
    minHeight: '28px',
    padding: '3px 8px',
    border: '0',
    borderRadius: t.rSm,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    fontWeight: '400',
    textAlign: 'left',
    whiteSpace: 'nowrap',
  },
  '& > li > button:hover': { background: t.bgHover, color: t.text },
  '& > li > button[aria-current="true"]': { background: t.bgActive, color: t.text },
  // items with a second line of metadata
  '&.two-line > li > button': { flexDirection: 'column', alignItems: 'stretch', gap: '2px', padding: '5px 8px' },
});

/** The content pane next to the side list. */
export const pane = css({ display: 'flex', flexDirection: 'column', minWidth: '0', minHeight: '0' });

export const paneHead = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  minHeight: '30px',
  padding: `0 ${t.s3}`,
  borderBottom: `1px solid ${t.border}`,
  fontSize: t.textSm,
  color: t.textMuted,
  flex: 'none',
  minWidth: '0',
  '& strong': { color: t.text, fontWeight: '500', fontFamily: t.mono, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
});

/** Quiet text inside a pane (loading, notices). */
export const paneNote = css({ margin: '0', padding: `${t.s3} ${t.s3}`, color: t.textMuted, fontSize: t.textMd });

/** Space around an ErrorNote inside a panel. */
export const errorSlot = css({ padding: `${t.s2} ${t.s2} 0`, flex: 'none', '&:empty': { display: 'none' } });

export const ellipsis = css({ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: '0' });

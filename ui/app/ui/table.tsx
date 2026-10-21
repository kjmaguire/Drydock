import { css } from 'remix/component';
import { t } from './tokens.ts';

/**
 * The one data table (the UI audit of 2026-10-19, A2 and C5): every cell has 12px above and below and 16px at each side, so two
 * columns are never closer than 32px, whatever page draws them. Put it inside `tableCard` (ui/layout.tsx) so it scrolls on its own
 * when the window is narrow; `td.num` and `th.num` right-align and keep digits in columns, `td.nowrap` keeps one line.
 */
export const dataTable = css({
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: t.textSm,
  '& th, & td': { textAlign: 'left', verticalAlign: 'top', padding: `${t.s3} ${t.s4}`, borderBottom: `1px solid ${t.border}` },
  '& th': { fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase', color: t.textMuted, whiteSpace: 'nowrap', background: t.bgRaised },
  '& td.num, & th.num': { textAlign: 'right', fontVariantNumeric: 'tabular-nums' },
  '& td.nowrap': { whiteSpace: 'nowrap' },
  '& td.mono': { fontFamily: t.mono },
  '& tbody tr:last-child td': { borderBottom: '0' },
});

// Redaction counts by rule (names and counts only; no scanned text is ever shown). Shared with the support bundle screen.

import { css, type Handle } from 'remix/component';
import { t } from '../../ui/tokens.ts';

const listCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: t.s1,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '4px',
    height: '22px',
    padding: '0 8px',
    borderRadius: t.rFull,
    background: t.bgActive,
    fontSize: t.textSm,
  },
  '& code': { background: 'transparent', padding: '0' },
});

export function RuleCounts(handle: Handle<{ by: Record<string, number> | undefined }>) {
  return () => {
    const entries = Object.entries(handle.props.by ?? {});
    if (entries.length === 0) return null;
    return (
      <ul mix={listCss} aria-label="Redactions by rule">
        {entries.map(([k, n]) => (
          <li key={k}>
            <code>{k}</code>: {n}
          </li>
        ))}
      </ul>
    );
  };
}

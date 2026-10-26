import { css, type Handle, type RemixNode } from 'remix/component';
import { t } from './tokens.ts';

const disclosureCss = css({
  minWidth: '0',
  '&[data-wide]': { gridColumn: '1 / -1' },
  '& > summary': { cursor: 'pointer', minHeight: '28px', display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textSm, fontWeight: '500', color: t.textMuted, listStyle: 'none' },
  '& > summary::-webkit-details-marker': { display: 'none' },
  // a flex summary loses the browser's triangle, so a closed group read as a stray line of text ("Advanced", "Details"; the UI
  // audit of 2026-10-26): a chevron says it opens, and turns down when it is open
  '& > summary::before': {
    content: '""',
    flex: 'none',
    width: '6px',
    height: '6px',
    marginLeft: '2px',
    borderRight: '1.5px solid currentColor',
    borderBottom: '1.5px solid currentColor',
    transform: 'rotate(-45deg)',
    transition: 'transform var(--duration-fast) var(--ease-out)',
  },
  '&[open] > summary::before': { transform: 'rotate(45deg)' },
  '& > summary:hover': { color: t.text },
  '& > .disclosure-body': { display: 'grid', gap: t.s2, marginTop: t.s2, minWidth: '0' },
  '& .disclosure-body p': { margin: '0' },
});

/**
 * One closed-by-default group of detail (the UI audit of 2026-10-19, C5): a summary line, the detail under it. The pages used to
 * style their own `<details>` four times over. A page puts here what is worth having at hand but not worth reading on arrival.
 */
export function Disclosure(handle: Handle<{ summary: RemixNode; open?: boolean; wide?: boolean; testId?: string; children?: RemixNode }>) {
  return () => {
    const { summary, open, wide, testId, children } = handle.props;
    return (
      <details mix={disclosureCss} open={open} data-wide={wide ? '' : undefined} data-testid={testId}>
        <summary>{summary}</summary>
        <div class="disclosure-body">{children}</div>
      </details>
    );
  };
}

import { css, type Handle, type RemixNode } from 'remix/component';
import { t } from './tokens.ts';

const disclosureCss = css({
  minWidth: '0',
  '&[data-wide]': { gridColumn: '1 / -1' },
  '& > summary': { cursor: 'pointer', minHeight: '28px', display: 'flex', alignItems: 'center', fontSize: t.textSm, fontWeight: '500', color: t.textMuted },
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

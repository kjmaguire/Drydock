import { css, type Handle, type RemixNode } from 'remix/component';
import { Icon, type IconNode } from './icon.tsx';
import { t } from './tokens.ts';

const pageCss = css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: '0', minWidth: '0' });

/** The bar of a full-height screen (a room, a chat): a 56px row with the title, used by the conversation screens. */
export const headerCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flex: 'none',
  height: '56px',
  padding: `0 ${t.s5}`,
  borderBottom: `1px solid ${t.border}`,
  background: t.bgApp,
  '& h1': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    minWidth: '0',
    fontSize: t.textLg,
    fontWeight: '650',
    letterSpacing: '-0.01em',
    whiteSpace: 'nowrap',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
  },
});

// A page's head sits in the page itself (Crew HQ): a large title, the subtitle under it, the actions on the right.
const pageHead = css({
  display: 'flex',
  alignItems: 'flex-end',
  gap: t.s4,
  flexWrap: 'wrap',
  '& h1': { display: 'flex', alignItems: 'center', gap: t.s3, minWidth: '0', fontSize: '28px', fontWeight: '700', letterSpacing: '-0.025em', lineHeight: '1.15' },
});
const headerIcon = css({ display: 'inline-flex', color: t.textMuted });
const headerSub = css({ color: t.textMuted, fontSize: t.textMd, marginTop: '6px', maxWidth: '80ch' });
const headerActions = css({ display: 'flex', alignItems: 'center', gap: t.s2, marginLeft: 'auto', flex: 'none', flexWrap: 'wrap' });

const bodyCss = css({ flex: '1', minHeight: '0', overflow: 'auto' });
const innerCss = css({ display: 'grid', gap: t.s6, padding: `${t.s6} ${t.s8} 56px`, maxWidth: '1320px', margin: '0 auto', width: '100%' });
const wideCss = css({ maxWidth: 'none' });

/**
 * A screen: a header bar with the title (h1), an optional subtitle and actions, then a scrolling body. `wide` lets the body
 * use the full width (tables, boards). `head` draws the page's head itself (an agent's hero beside its crew list): it is
 * given the id the page's h1 must carry, and replaces the default header and the children.
 */
export function Page(
  handle: Handle<{
    title: RemixNode;
    icon?: IconNode;
    subtitle?: RemixNode;
    actions?: RemixNode;
    wide?: boolean;
    flush?: boolean;
    children?: RemixNode;
    testId?: string;
    head?: (titleId: string) => RemixNode;
  }>,
) {
  const titleId = `page-title-${handle.id}`;
  return () => {
    const { title, icon, subtitle, actions, wide, flush, children, testId, head } = handle.props;
    if (head) {
      return (
        <section mix={pageCss} aria-labelledby={titleId} data-testid={testId}>
          <div mix={bodyCss}>
            <div mix={[innerCss, wide ? wideCss : undefined]}>{head(titleId)}</div>
          </div>
        </section>
      );
    }
    return (
      <section mix={pageCss} aria-labelledby={titleId} data-testid={testId}>
        {flush ? (
          <header mix={headerCss}>
            <h1 id={titleId}>
              {icon ? (
                <span mix={headerIcon}>
                  <Icon icon={icon} size={16} />
                </span>
              ) : null}
              {title}
            </h1>
            {subtitle ? <div mix={headerSub} style={{ marginTop: '0' }}>{subtitle}</div> : null}
            {actions ? <div mix={headerActions}>{actions}</div> : null}
          </header>
        ) : null}
        <div mix={bodyCss}>
          {flush ? (
            children
          ) : (
            <div mix={[innerCss, wide ? wideCss : undefined]}>
              <header mix={pageHead}>
                <div style={{ minWidth: '0' }}>
                  <h1 id={titleId}>
                    {icon ? (
                      <span mix={headerIcon}>
                        <Icon icon={icon} size={22} />
                      </span>
                    ) : null}
                    {title}
                  </h1>
                  {subtitle ? <div mix={headerSub}>{subtitle}</div> : null}
                </div>
                {actions ? <div mix={headerActions}>{actions}</div> : null}
              </header>
              {children}
            </div>
          )}
        </div>
      </section>
    );
  };
}

const sectionCss = css({ display: 'grid', gap: t.s3, minWidth: '0' });
const sectionHead = css({
  display: 'flex',
  alignItems: 'flex-end',
  justifyContent: 'space-between',
  gap: t.s3,
  '& h2': { fontSize: t.textLg, fontWeight: '650', letterSpacing: '-0.01em', margin: '0' },
  '& h3': { fontSize: t.textMd, fontWeight: '600', margin: '0' },
  '& p': { color: t.textMuted, fontSize: t.textSm, marginTop: '3px' },
});

/** A titled group within a page. `level` 3 is a group inside another section (an h3 under the page's h2 groups). */
export function Section(
  handle: Handle<{ title?: RemixNode; description?: RemixNode; actions?: RemixNode; children?: RemixNode; label?: string; testId?: string; level?: 2 | 3 }>,
) {
  return () => {
    const { title, description, actions, children, label, testId, level = 2 } = handle.props;
    return (
      <section mix={sectionCss} aria-label={label} data-testid={testId}>
        {title || actions ? (
          <div mix={sectionHead}>
            <div>
              {title ? level === 3 ? <h3>{title}</h3> : <h2>{title}</h2> : null}
              {description ? <p>{description}</p> : null}
            </div>
            {actions ? <div>{actions}</div> : null}
          </div>
        ) : null}
        {children}
      </section>
    );
  };
}

/** A bordered surface. */
export const card = css({
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  padding: '18px',
  minWidth: '0',
});

/** A bordered surface whose rows are separated by lines (a list of items). */
export const listCard = css({
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li, & > .row': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s3,
    padding: `11px 18px`,
    borderTop: `1px solid ${t.border}`,
    minWidth: '0',
  },
  '& > li:first-child, & > .row:first-child': { borderTop: '0' },
});

/** A table inside a bordered surface. Wrap a <table> with this. */
export const tableCard = css({
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'auto',
  '& tr:last-child td': { borderBottom: '0' },
});

/** Horizontal row with a gap; wraps. */
export const row = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', minWidth: '0' });
/** Vertical stack with a gap. */
export const stack = css({ display: 'grid', gap: t.s3, minWidth: '0' });
/** Push following items to the end of a row. */
export const spacer = css({ flex: '1' });
/** A responsive grid of cards. */
export const cardGrid = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: t.s3 });

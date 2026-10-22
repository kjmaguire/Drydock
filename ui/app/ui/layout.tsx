import { css, type Handle, type RemixNode } from 'remix/component';
import { Icon, type IconNode } from './icon.tsx';
import type { GuideId } from '../screens/guide/topics.ts';
import { LearnMore } from './learn-more.tsx';
import { t } from './tokens.ts';

const pageCss = css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: '0', minWidth: '0' });

/** The bar on top of every screen (graphite, P97): the title, what it is about, its own tabs or filters, the actions at the end. */
export const headerCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: `${t.s2} ${t.s3}`,
  flex: 'none',
  minHeight: 'var(--header-height)',
  padding: `${t.s2} ${t.s6}`,
  borderBottom: `1px solid ${t.border}`,
  background: t.bgApp,
  '& h1': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    minWidth: '0',
    fontSize: t.textLg,
    fontWeight: '600',
    letterSpacing: '-0.005em',
    whiteSpace: 'nowrap',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
  },
});

const headerIcon = css({ display: 'inline-flex', color: t.textMuted });
const headerSub = css({ color: t.textFaint, fontSize: t.textMd, minWidth: '0', flex: '0 1 auto', maxWidth: '90ch' });
const headerBar = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: `${t.s2} ${t.s3}`, minWidth: '0' });
const headerActions = css({ display: 'flex', alignItems: 'center', gap: t.s2, marginLeft: 'auto', flex: 'none', flexWrap: 'wrap' });

const bodyCss = css({ flex: '1', minHeight: '0', overflow: 'auto' });
const innerCss = css({ display: 'grid', gap: t.s5, padding: `${t.s5} ${t.s6} 48px`, maxWidth: '1320px', width: '100%' });
const wideCss = css({ maxWidth: 'none' });

/**
 * A screen: a header bar with the title (h1), an optional subtitle and actions, then a scrolling body. `wide` lets the body
 * use the full width (tables, boards). `head` draws the page's head itself (an agent's hero beside its crew list): it is
 * given the id the page's h1 must carry, and replaces the default header and the children; with `flush` it also replaces the
 * scrolling body (the page draws its own bar and scrolls its own columns).
 */
export function Page(
  handle: Handle<
    {
      title: RemixNode;
      subtitle?: RemixNode;
      actions?: RemixNode;
      wide?: boolean;
      flush?: boolean;
      children?: RemixNode;
      testId?: string;
      /** The Guide topic that explains this page: a "Learn more" link beside the subtitle (the page itself keeps to the basics). */
      guide?: GuideId;
      /** The page's own view tabs and filters in the bar, after the title (a board's Board / List / Timeline and its filter chips). */
      bar?: RemixNode;
    } & (
      // every page title carries the icon of what the page is, or the mark of what it belongs to (a project's colour square);
      // only a page that draws its own head (an agent) may leave both out
      | { icon: IconNode; mark?: undefined; head?: undefined }
      | { icon?: undefined; mark: RemixNode; head?: undefined }
      | { icon?: IconNode; mark?: undefined; head: (titleId: string) => RemixNode }
    )
  >,
) {
  const titleId = `page-title-${handle.id}`;
  return () => {
    const { title, icon, mark, subtitle, actions, wide, flush, children, testId, head, guide, bar } = handle.props;
    const sub = subtitle || guide ? <>{subtitle}{subtitle && guide ? ' ' : null}{guide ? <LearnMore topic={guide} /> : null}</> : null;
    // a page that lays itself out to the edges (the task page: its own bar, columns and scrolling) draws everything in `head`
    if (head && flush) {
      return (
        <section mix={pageCss} aria-labelledby={titleId} data-testid={testId}>
          {head(titleId)}
        </section>
      );
    }
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
        <header mix={headerCss}>
          <h1 id={titleId}>
            {icon ? (
              <span mix={headerIcon}>
                <Icon icon={icon} size={16} />
              </span>
            ) : null}
            {mark ?? null}
            {title}
          </h1>
          {sub ? <div mix={headerSub}>{sub}</div> : null}
          {bar ? <div mix={headerBar}>{bar}</div> : null}
          {actions ? <div mix={headerActions}>{actions}</div> : null}
        </header>
        <div mix={bodyCss}>{flush ? children : <div mix={[innerCss, wide ? wideCss : undefined]}>{children}</div>}</div>
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
  '& h2': { fontSize: t.textMd, fontWeight: '600', margin: '0' },
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
  padding: '14px',
  minWidth: '0',
});

/** A bordered list with no row layout of its own: the rows draw themselves (a task list, a memory list). */
export const listBox = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
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
    padding: `10px ${t.s4}`,
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

/** A plain list as a column of rows with a small gap (no bullets, no indent). */
export const stackList = css({ display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });
/** A plain list whose rows nearly touch (a menu of links). */
export const tightList = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none' });
/** The actions of a bar or a header, pushed to its end. */
export const actionsEnd = css({ display: 'flex', alignItems: 'center', gap: t.s2, marginLeft: 'auto', flex: 'none' });

/** Horizontal row with a gap; wraps. */
export const row = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', minWidth: '0' });
/** Vertical stack with a gap. */
export const stack = css({ display: 'grid', gap: t.s3, minWidth: '0' });
/** Push following items to the end of a row. */
export const spacer = css({ flex: '1' });
/** A responsive grid of cards. */
export const cardGrid = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: t.s3 });

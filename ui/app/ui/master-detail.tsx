// The list-and-detail layout of the settings-like pages (decision P98: Agents, Integrations, Models): a filterable list of
// small rows on the left, grouped under quiet headings, and the chosen row's detail on the right. Below 1024 px the list
// fills the page and a chosen row opens its detail in its place, with a Back button. The detail is drawn from the same few
// parts: DetailHeader, SettingsGroup with SettingRows, FactGrid.

import { css, on, ref, type Handle, type RemixNode } from 'remix/component';
import { ArrowLeft, Search } from 'lucide';
import { BrandLogo, brandKey } from './brand-logo.tsx';
import { typing } from '../lib/keys.ts';
import { Icon, type IconNode } from './icon.tsx';
import { t, tone as tones, type Tone } from './tokens.ts';

/** Narrower than this the list and the detail take turns (the sidebar takes the rest of a 1280 px window). */
export const SPLIT_NARROW = '(max-width: 1023px)';

export interface ListItem {
  key: string;
  title: string;
  /** A brand id (BrandLogo); a thing without a brand shows `icon` on a neutral square. */
  logo?: string;
  icon?: IconNode;
  /** One line saying what it is or how it is set up. */
  sub?: string;
  /** Its state in a word or two, at the end of the row ("On", "Set up", "Key expired"). */
  status?: { text: string; tone?: Tone | 'none' };
  /** A row that opens a page of its own (GitHub) instead of a detail beside the list. */
  href?: string;
  /** Dimmed (not installed). */
  quiet?: boolean;
  testId?: string;
  data?: Record<string, string>;
}

export interface ListGroup {
  key: string;
  title: string;
  /** A word or two after the heading ("app sync", "MCP"). */
  note?: string;
  items: ListItem[];
  /** After the group's rows: an "Add a server" row. */
  footer?: RemixNode;
}

export interface ListFilter {
  key: string;
  label: string;
  count?: number;
}

const rootCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(260px, 320px) minmax(0, 1fr)',
  height: '100%',
  minHeight: '0',
  [`@media ${SPLIT_NARROW}`]: {
    gridTemplateColumns: 'minmax(0, 1fr)',
    '&[data-open="true"] > [data-part="list"]': { display: 'none' },
    '&[data-open="false"] > [data-part="detail"]': { display: 'none' },
  },
});

const listCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: t.s2,
  minWidth: '0',
  minHeight: '0',
  overflow: 'auto',
  padding: `${t.s3} ${t.s3} ${t.s6}`,
  borderRight: `1px solid ${t.border}`,
  [`@media ${SPLIT_NARROW}`]: { borderRight: '0' },
});

const searchCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '32px',
  padding: `0 ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgInput,
  color: t.textMuted,
  '&:focus-within': { borderColor: t.accent, boxShadow: t.focus },
  '& input': { flex: '1', minWidth: '0', height: '30px', border: '0', outline: '0', padding: '0', background: 'transparent', color: t.text, font: 'inherit', fontSize: t.textMd },
  '& input:focus': { boxShadow: 'none' },
});

const kbdCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  minWidth: '18px',
  height: '18px',
  padding: '0 4px',
  border: `1px solid ${t.border}`,
  borderRadius: '4px',
  background: t.bgRaised,
  color: t.textMuted,
  fontFamily: t.mono,
  fontSize: t.textXs,
});

const filtersCss = css({
  display: 'flex',
  gap: '2px',
  padding: '2px',
  borderRadius: t.rMd,
  background: t.bgInput,
  '& button': {
    flex: '1',
    height: '26px',
    padding: `0 ${t.s2}`,
    border: '0',
    borderRadius: t.rSm,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    fontWeight: '500',
  },
  '& button:hover': { color: t.text, background: 'transparent' },
  '& button[aria-pressed="true"]': { background: t.bgActive, color: t.text, boxShadow: t.shadowSm },
});

const groupCss = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none' });

const groupHeadCss = css({
  display: 'flex',
  alignItems: 'baseline',
  justifyContent: 'space-between',
  gap: t.s2,
  margin: `${t.s3} ${t.s2} ${t.s1}`,
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '600',
  letterSpacing: '0.06em',
  textTransform: 'uppercase',
  '& span': { fontWeight: '500', letterSpacing: '0', textTransform: 'none', color: t.textFaint },
});

/** One row of the list: the logo, the name over one line, the state at the end. A button, or a link for a row with a page. */
export const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'flex-start',
  gap: t.s3,
  width: '100%',
  minHeight: '44px',
  padding: `6px ${t.s2}`,
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.text,
  textAlign: 'left',
  textDecoration: 'none',
  whiteSpace: 'normal',
  cursor: 'pointer',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-current="true"]': { background: t.bgActive },
  '&[data-quiet="true"]': { color: t.textMuted },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& [data-part="text"]': { display: 'flex', flexDirection: 'column', minWidth: '0', flex: '1 1 auto' },
  '& [data-part="title"]': { fontWeight: '500', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& [data-part="sub"]': { color: t.textMuted, fontSize: t.textSm, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
});

const glyphCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flex: 'none',
  borderRadius: '6px',
  background: t.bgApp,
  color: t.textMuted,
  boxShadow: `inset 0 0 0 1px ${t.border}`,
});

const detailCss = css({ minWidth: '0', minHeight: '0', overflow: 'auto' });
const detailInner = css({ display: 'grid', gap: t.s5, alignContent: 'start', maxWidth: '980px', padding: `${t.s5} ${t.s6} 48px` });

const backCss = css({
  display: 'none',
  alignItems: 'center',
  gap: t.s1,
  justifySelf: 'start',
  height: '28px',
  padding: `0 ${t.s2}`,
  border: '0',
  borderRadius: t.rSm,
  background: 'transparent',
  color: t.textMuted,
  [`@media ${SPLIT_NARROW}`]: { display: 'inline-flex' },
});

/** A brand logo, or an icon on a neutral square, at `size` px. */
export function ItemMark(handle: Handle<{ logo?: string; icon?: IconNode; size?: number }>) {
  return () => {
    const { logo, icon, size = 24 } = handle.props;
    if (logo && brandKey(logo)) return <BrandLogo id={logo} size={size} />;
    return (
      <span mix={glyphCss} style={{ width: `${size}px`, height: `${size}px` }} aria-hidden="true">
        {icon ? <Icon icon={icon} size={Math.round(size * 0.55)} /> : null}
      </span>
    );
  };
}

const dotCss = css({ display: 'inline-block', width: '7px', height: '7px', borderRadius: '50%', flex: 'none' });
const statusCss = css({ display: 'inline-flex', alignItems: 'center', gap: '6px', flex: 'none', marginLeft: 'auto', fontSize: t.textSm, whiteSpace: 'nowrap' });

/** A state in words with a coloured dot before it (the dot never stands alone). `none`: the words only, muted. */
export function StatusWord(handle: Handle<{ text: string; tone?: Tone | 'none'; testId?: string }>) {
  return () => {
    const { text, tone = 'muted', testId } = handle.props;
    const fg = tone === 'none' || tone === 'muted' ? t.textMuted : tones[tone].fg;
    return (
      <span mix={statusCss} style={{ color: fg }} data-testid={testId} data-tone={tone}>
        {tone === 'none' ? null : <span mix={dotCss} style={{ background: tone === 'muted' ? t.textFaint : tones[tone].fg }} aria-hidden="true"></span>}
        {text}
      </span>
    );
  };
}

function matches(it: ListItem, q: string): boolean {
  if (!q) return true;
  const s = q.toLowerCase();
  return it.title.toLowerCase().includes(s) || (it.sub ?? '').toLowerCase().includes(s);
}

/** The rows the list shows for a query, group by group (a group with none left is not shown). */
export function visibleGroups(groups: ListGroup[], q: string): ListGroup[] {
  return groups.map((g) => ({ ...g, items: g.items.filter((it) => matches(it, q.trim())) })).filter((g) => g.items.length > 0 || (!q.trim() && g.footer));
}

/**
 * The layout. `selected` is the chosen row's key and `onSelect` chooses one (the page keeps the choice, so a link can open a
 * row); `detail` is the chosen row's detail. `search` turns on the filter box (`/` focuses it, the arrow keys then move
 * through the rows); `filters` are the segmented choices above the list (All, On, Needs you), owned by the page. `address`
 * is the key the page's address names, when it names one: a narrow window shows that row's detail as soon as the page has
 * chosen it, whether the page opened at the address or a link changed it while the page stayed mounted, and the plain address
 * (no key) shows the list again. Only the addressed row opens that way: a page opens a row of its own accord (an "Add" row,
 * the row left after a removal) by putting it in the address, so a row the page drew in place of a key that is gone does not
 * open, and the person sees the list.
 */
export function MasterDetail(
  handle: Handle<{
    label: string;
    groups: ListGroup[];
    selected?: string;
    onSelect: (key: string) => void;
    detail: RemixNode;
    search?: string;
    filters?: ListFilter[];
    filter?: string;
    onFilter?: (key: string) => void;
    /** Under the list: anything the page adds (a link to the Library). */
    listFooter?: RemixNode;
    /**
     * The key the address names (/integrations/linear) when it names one: a narrow window shows that row's detail once the page
     * has chosen it, also when the address changes while the page stays mounted; the plain address shows the list again. A key
     * the page replaced with its default row (the addressed item is gone) opens nothing.
     */
    address?: string;
    testId?: string;
  }>,
) {
  let query = '';
  // narrow windows: whether the detail is showing in place of the list (a choice opens it, Back closes it)
  let open = false;
  // the address the layout last saw, and whether its row's detail is still to be shown: a new address waits for the page to
  // choose its row (a page may choose after loading), and only that row opens. A page opens a row of its own accord (an "Add"
  // row, the row left after a removal) by putting it in the address, so a default it draws in place of a row that is gone (the
  // key of a bookmark, an install that went away on a re-read) opens nothing: the person sees the list.
  let lastAddress: string | undefined;
  let addressPending = false;
  let searchEl: HTMLInputElement | null = null;
  let listEl: HTMLElement | null = null;

  const choose = (key: string, focus: boolean) => {
    open = true;
    handle.props.onSelect(key);
    void handle.update();
    if (focus) handle.queueTask(() => listEl?.querySelector<HTMLElement>(`[data-key="${CSS.escape(key)}"]`)?.focus());
  };

  const keys = (): string[] => visibleGroups(handle.props.groups, query).flatMap((g) => g.items.filter((it) => !it.href).map((it) => it.key));

  const onListKey = (e: KeyboardEvent) => {
    if (e.defaultPrevented || (e.key !== 'ArrowDown' && e.key !== 'ArrowUp')) return;
    const ks = keys();
    if (ks.length === 0) return;
    e.preventDefault();
    const at = ks.indexOf(handle.props.selected ?? '');
    const next = e.key === 'ArrowDown' ? ks[Math.min(ks.length - 1, at + 1)] : ks[Math.max(0, at < 0 ? 0 : at - 1)];
    if (next !== undefined) choose(next, true);
  };

  const onRootKey = (e: KeyboardEvent) => {
    if (e.key !== '/' || !searchEl || typing(e)) return;
    e.preventDefault();
    searchEl.focus();
  };

  return () => {
    const { label, groups, selected, detail, search, filters, filter, onFilter, listFooter, address, testId } = handle.props;
    if (address !== lastAddress) {
      lastAddress = address;
      addressPending = !!address;
      // the plain address (the page's own link, no key) shows the list again
      if (!address) open = false;
    }
    if (addressPending && selected) {
      addressPending = false;
      if (selected === address) open = true;
    }
    const shown = visibleGroups(groups, query);
    return (
      <div mix={[rootCss, on<HTMLDivElement, 'keydown'>('keydown', onRootKey)]} data-open={String(open)} data-testid={testId}>
        <nav aria-label={label} data-part="list" mix={[listCss, on<HTMLElement, 'keydown'>('keydown', onListKey), ref((el) => (listEl = el as HTMLElement))]}>
          {search ? (
            <label mix={searchCss}>
              <Icon icon={Search} size={13} />
              <input
                type="search"
                placeholder={search}
                aria-label={search}
                value={query}
                data-testid="list-filter"
                mix={[
                  ref((el) => (searchEl = el as HTMLInputElement)),
                  on('input', (e) => ((query = (e.currentTarget as HTMLInputElement).value), void handle.update())),
                  on('keydown', (e) => {
                    if (e.key === 'ArrowDown') {
                      const first = keys()[0];
                      // the first row, and only it: the list's own arrow handling must not move on from there
                      if (first !== undefined) (e.preventDefault(), e.stopPropagation(), choose(first, true));
                    } else if (e.key === 'Escape' && query) {
                      e.stopPropagation();
                      query = '';
                      void handle.update();
                    }
                  }),
                ]}
              />
              <span mix={kbdCss} aria-hidden="true">/</span>
            </label>
          ) : null}
          {filters && filters.length > 0 ? (
            <div mix={filtersCss} role="group" aria-label="Show">
              {filters.map((f) => (
                <button key={f.key} type="button" aria-pressed={f.key === filter ? 'true' : 'false'} data-testid={`list-filter-${f.key}`} mix={on('click', () => onFilter?.(f.key))}>
                  {f.label}
                  {f.count !== undefined ? ` ${f.count}` : ''}
                </button>
              ))}
            </div>
          ) : null}
          {shown.length === 0 && query.trim() ? <p mix={css({ margin: `${t.s3} ${t.s2}`, color: t.textMuted })}>Nothing matches "{query.trim()}".</p> : null}
          {shown.map((g) => (
            <section key={g.key} aria-label={g.title} data-testid={`list-group-${g.key}`}>
              <h2 mix={groupHeadCss}>
                {g.title}
                {g.note ? <span>{g.note}</span> : null}
              </h2>
              <ul mix={groupCss}>
                {g.items.map((it) => {
                  const attrs: Record<string, string> = {};
                  for (const [k, v] of Object.entries(it.data ?? {})) attrs[`data-${k}`] = v;
                  const body = (
                    <>
                      <ItemMark logo={it.logo} icon={it.icon} />
                      <span data-part="text">
                        <span data-part="title">{it.title}</span>
                        {it.sub ? <span data-part="sub">{it.sub}</span> : null}
                      </span>
                      {it.status ? <StatusWord text={it.status.text} tone={it.status.tone} /> : null}
                    </>
                  );
                  return (
                    <li key={it.key}>
                      {it.href ? (
                        <a href={it.href} mix={rowCss} data-key={it.key} data-testid={it.testId} data-quiet={it.quiet ? 'true' : undefined} {...attrs}>
                          {body}
                        </a>
                      ) : (
                        <button
                          type="button"
                          mix={[rowCss, on('click', () => choose(it.key, false))]}
                          aria-current={it.key === selected ? 'true' : undefined}
                          data-key={it.key}
                          data-testid={it.testId}
                          data-quiet={it.quiet ? 'true' : undefined}
                          {...attrs}
                        >
                          {body}
                        </button>
                      )}
                    </li>
                  );
                })}
                {!query.trim() && g.footer ? <li>{g.footer}</li> : null}
              </ul>
            </section>
          ))}
          {listFooter ?? null}
        </nav>
        <div data-part="detail" mix={detailCss}>
          <div mix={detailInner}>
            <button type="button" mix={[backCss, on('click', () => ((open = false), void handle.update()))]} data-testid="detail-back">
              <Icon icon={ArrowLeft} size={14} />
              Back to the list
            </button>
            {detail}
          </div>
        </div>
      </div>
    );
  };
}

const headCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: t.s3,
  '& h2': { margin: '0', fontSize: t.textXl, fontWeight: '600', letterSpacing: '-0.005em' },
});
const headText = css({ display: 'grid', gap: '2px', minWidth: '0', flex: '1 1 240px' });
const headTitle = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2 });
const headSub = css({ color: t.textMuted, fontSize: t.textSm });
const headActions = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2, marginLeft: 'auto' });
const pillCss = css({ display: 'inline-flex', alignItems: 'center', gap: '5px', height: '20px', padding: '0 8px', borderRadius: t.rFull, fontSize: t.textXs, fontWeight: '600', whiteSpace: 'nowrap' });

/** The top of a detail: the logo, the name (h2) with its state as a pill, one line under it, the actions at the end. */
export function DetailHeader(
  handle: Handle<{ title: RemixNode; logo?: string; icon?: IconNode; status?: { text: string; tone: Tone }; sub?: RemixNode; actions?: RemixNode; testId?: string }>,
) {
  return () => {
    const { title, logo, icon, status, sub, actions, testId } = handle.props;
    const c = status ? tones[status.tone] : undefined;
    return (
      <div mix={headCss} data-testid={testId}>
        <ItemMark logo={logo} icon={icon} size={36} />
        <div mix={headText}>
          <div mix={headTitle}>
            <h2>{title}</h2>
            {status && c ? (
              <span mix={pillCss} style={{ color: c.fg, background: c.bg }} data-testid="detail-status" data-tone={status.tone}>
                <span mix={dotCss} style={{ background: c.fg }} aria-hidden="true"></span>
                {status.text}
              </span>
            ) : null}
          </div>
          {sub ? <div mix={headSub}>{sub}</div> : null}
        </div>
        {actions ? <div mix={headActions}>{actions}</div> : null}
      </div>
    );
  };
}

const groupBox = css({ display: 'grid', gap: t.s2, minWidth: '0' });
const groupBar = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: t.s2,
  '& h3': { margin: '0', fontSize: t.textSm, fontWeight: '600', color: t.textMuted },
  '& > :last-child:not(h3)': { marginLeft: 'auto' },
});
const groupCard = css({ border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface, overflow: 'hidden', minWidth: '0' });

/** A titled group of settings rows on one surface (the rows draw the lines between them). */
export function SettingsGroup(handle: Handle<{ title?: RemixNode; actions?: RemixNode; children?: RemixNode; testId?: string; label?: string; bare?: boolean }>) {
  return () => {
    const { title, actions, children, testId, label, bare } = handle.props;
    return (
      <section mix={groupBox} aria-label={label ?? (typeof title === 'string' ? title : undefined)} data-testid={testId}>
        {title || actions ? (
          <div mix={groupBar}>
            {title ? <h3>{title}</h3> : null}
            {actions ? <div>{actions}</div> : null}
          </div>
        ) : null}
        {bare ? children : <div mix={groupCard}>{children}</div>}
      </section>
    );
  };
}

const settingCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(120px, 170px) minmax(0, 1fr) auto',
  alignItems: 'center',
  gap: `${t.s1} ${t.s4}`,
  minHeight: '46px',
  padding: `${t.s2} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  '&:first-child': { borderTop: '0' },
  '& > [data-part="label"]': { color: t.textMuted, minWidth: '0' },
  '& > [data-part="value"]': { minWidth: '0', overflowWrap: 'anywhere' },
  '& > [data-part="control"]': { display: 'flex', alignItems: 'center', gap: t.s2, justifySelf: 'end' },
  '@media (max-width: 719px)': { gridTemplateColumns: 'minmax(0, 1fr) auto', '& > [data-part="value"]': { gridColumn: '1 / -1', gridRow: '2' } },
});

/** One setting: what it is, its value, and the control that changes it. */
export function SettingRow(handle: Handle<{ label: RemixNode; children?: RemixNode; control?: RemixNode; testId?: string }>) {
  return () => {
    const { label, children, control, testId } = handle.props;
    return (
      <div mix={settingCss} data-testid={testId}>
        <span data-part="label">{label}</span>
        <span data-part="value">{children}</span>
        <span data-part="control">{control}</span>
      </div>
    );
  };
}

const factsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
  margin: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > div': { display: 'grid', gap: '3px', padding: `${t.s3} ${t.s4}`, boxShadow: `inset 0 1px 0 ${t.border}, inset 1px 0 0 ${t.border}`, minWidth: '0' },
  '& dt': { color: t.textMuted, fontSize: t.textSm },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere' },
  '& dd[data-mono="true"]': { fontFamily: t.mono, fontSize: t.textSm },
});

/** Facts as a grid of label-over-value cells (a CLI's version, path and sign-in). */
export function FactGrid(handle: Handle<{ facts: { label: string; value: RemixNode; mono?: boolean }[]; label?: string; testId?: string }>) {
  return () => {
    const { facts, label, testId } = handle.props;
    return (
      <dl mix={factsCss} aria-label={label} data-testid={testId}>
        {facts.map((f) => (
          <div key={f.label}>
            <dt>{f.label}</dt>
            <dd data-mono={f.mono ? 'true' : undefined}>{f.value}</dd>
          </div>
        ))}
      </dl>
    );
  };
}

/** The "+ Add …" row at the end of a group, drawn like a row; `current` marks it chosen (its form is the detail). */
export function AddRow(handle: Handle<{ label: string; onClick?: () => void; href?: string; current?: boolean; testId?: string }>) {
  return () => {
    const { label, onClick, href, current, testId } = handle.props;
    const mark = <span mix={glyphCss} style={{ width: '24px', height: '24px', boxShadow: `inset 0 0 0 1px ${t.borderStrong}`, borderStyle: 'dashed' }} aria-hidden="true">+</span>;
    return href ? (
      <a href={href} mix={rowCss} data-quiet="true" data-testid={testId}>
        {mark}
        {label}
      </a>
    ) : (
      <button type="button" mix={[rowCss, on('click', () => onClick?.())]} aria-current={current ? 'true' : undefined} data-quiet="true" data-testid={testId}>
        {mark}
        {label}
      </button>
    );
  };
}

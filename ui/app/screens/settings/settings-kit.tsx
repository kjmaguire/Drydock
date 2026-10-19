// Settings-page building blocks shared by the workspace screens (Settings, License, Backups, Export and import, Support
// bundle): Linear-style groups with the title and description on the left and the controls on the right, panels of rows,
// a danger zone, and the small status and warning lines those pages use. Colors come only from tokens.

import { css, type Handle, type RemixNode } from 'remix/component';
import { TriangleAlert } from 'lucide';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';

/** The page column: narrower than a list page, left-aligned under the title, and a container for the two-column groups. */
export const settingsColumn = css({
  display: 'grid',
  gap: t.s8,
  width: '100%',
  maxWidth: '960px',
  margin: '0',
  containerType: 'inline-size',
});

/** The short paragraph under a page header that says what the page is for. */
export const intro = css({
  margin: '0',
  maxWidth: '720px',
  color: t.textMuted,
  fontSize: t.textMd,
  lineHeight: '1.55',
});

const groupCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(180px, 248px) minmax(0, 1fr)',
  gap: `${t.s3} ${t.s8}`,
  alignItems: 'start',
  paddingTop: t.s6,
  borderTop: `1px solid ${t.border}`,
  '&:first-child': { paddingTop: '0', borderTop: '0' },
  '@container (max-width: 720px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
});
const stackedCss = css({ gridTemplateColumns: 'minmax(0, 1fr)', '& > div:first-child': { maxWidth: '720px' } });

const groupHead = css({
  display: 'grid',
  gap: t.s1,
  alignContent: 'start',
  '& h2': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, fontWeight: '600', color: t.text },
  '& h2 svg': { color: t.textMuted },
  '& > p, & > div': { margin: '0', color: t.textMuted, fontSize: t.textSm, lineHeight: '1.55' },
});

const groupBody = css({ display: 'grid', gap: t.s3, minWidth: '0' });

/**
 * A settings group: title (h2) and description on the left, the controls on the right (`stacked` puts them under the title,
 * for wide tables). `label` names the region for assistive technology (and for the end-to-end tests).
 */
export function Group(
  handle: Handle<{ title: RemixNode; icon?: IconNode; description?: RemixNode; label?: string; testId?: string; stacked?: boolean; children?: RemixNode }>,
) {
  return () => {
    const { title, icon, description, label, testId, stacked, children } = handle.props;
    return (
      <section mix={[groupCss, stacked ? stackedCss : undefined]} aria-label={label} data-testid={testId}>
        <div mix={groupHead}>
          <h2>
            {icon ? <Icon icon={icon} size={15} /> : null}
            {title}
          </h2>
          {description ? <div>{description}</div> : null}
        </div>
        <div mix={groupBody}>{children}</div>
      </section>
    );
  };
}

/** A bordered surface whose children are separated by lines (rows of settings). */
export const panel = css({
  display: 'grid',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  overflow: 'hidden',
  '& > *': { padding: `${t.s3} ${t.s4}`, borderTop: `1px solid ${t.border}`, minWidth: '0' },
  '& > *:first-child': { borderTop: '0' },
});

/** A panel whose single child carries its own padding (a form, a table). */
export const panelPad = css({
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  padding: t.s4,
  minWidth: '0',
});

const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: t.s4,
  flexWrap: 'wrap',
  '& .txt': { display: 'grid', gap: '2px', minWidth: '0', flex: '1 1 260px' },
  '& .lbl': { fontSize: t.textMd, fontWeight: '500', color: t.text },
  '& .desc': { fontSize: t.textSm, color: t.textMuted, lineHeight: '1.5' },
  '& .ctl': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', flex: 'none', maxWidth: '100%' },
});

/** One row of a panel: what it is (label and description) on the left, the control on the right. */
export function Row(handle: Handle<{ label: RemixNode; description?: RemixNode; children?: RemixNode; testId?: string }>) {
  return () => {
    const { label, description, children, testId } = handle.props;
    return (
      <div mix={rowCss} data-testid={testId}>
        <div class="txt">
          <span class="lbl">{label}</span>
          {description ? <span class="desc">{description}</span> : null}
        </div>
        {children ? <div class="ctl">{children}</div> : null}
      </div>
    );
  };
}

const dangerCss = css({
  display: 'grid',
  border: `1px solid color-mix(in srgb, ${t.danger} 40%, ${t.border})`,
  borderRadius: t.rLg,
  background: `color-mix(in srgb, ${t.dangerSoft} 35%, ${t.bgSurface})`,
  overflow: 'hidden',
  '& > *': { padding: `${t.s3} ${t.s4}`, borderTop: `1px solid color-mix(in srgb, ${t.danger} 25%, ${t.border})` },
  '& > *:first-child': { borderTop: '0' },
});

/** Destructive actions, set apart in red. Each action inside asks for confirmation before it runs. */
export function DangerZone(handle: Handle<{ children?: RemixNode; label?: string }>) {
  return () => (
    <div mix={dangerCss} role="group" aria-label={handle.props.label ?? 'Danger zone'}>
      {handle.props.children}
    </div>
  );
}

const calloutCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  borderRadius: t.rLg,
  border: '1px solid',
  fontSize: t.textMd,
  lineHeight: '1.55',
  '& h2, & h3': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, fontWeight: '600' },
  '& p': { margin: '0' },
  '& ul': { margin: '0', paddingLeft: '18px', display: 'grid', gap: '2px' },
});

/** A bordered, tinted box for a state the person must know about (a staged restore, read-only, an unconfigured build). */
export function Callout(
  handle: Handle<{ tone?: Tone; icon?: IconNode; role?: 'status' | 'note' | 'alert'; testId?: string; title?: RemixNode; level?: 2 | 3; children?: RemixNode }>,
) {
  return () => {
    const { tone: tn = 'info', icon, role, testId, title, level = 2, children } = handle.props;
    const c = tone[tn];
    const head = title ? (
      <>
        {icon ? (
          <span style={{ color: c.fg, display: 'inline-flex' }}>
            <Icon icon={icon} size={15} />
          </span>
        ) : null}
        {title}
      </>
    ) : null;
    return (
      <div
        mix={calloutCss}
        role={role}
        data-testid={testId}
        style={{ background: c.bg, borderColor: `color-mix(in srgb, ${c.fg} 30%, transparent)` }}
      >
        {head ? level === 2 ? <h2>{head}</h2> : <h3>{head}</h3> : null}
        {children}
      </div>
    );
  };
}

const warnCss = css({
  display: 'flex',
  gap: t.s2,
  alignItems: 'flex-start',
  margin: '0',
  padding: '8px 12px',
  borderRadius: t.rMd,
  background: t.warningSoft,
  color: t.text,
  fontSize: t.textMd,
  '& > svg': { color: t.warning, marginTop: '2px' },
  '& ul': { margin: '4px 0 0', paddingLeft: '18px' },
});

/** A warning line (role="note" unless told otherwise). */
export function Warn(handle: Handle<{ testId?: string; role?: 'note' | 'alert' | 'status'; children?: RemixNode }>) {
  return () => (
    <div mix={warnCss} role={handle.props.role ?? 'note'} data-testid={handle.props.testId}>
      <Icon icon={TriangleAlert} size={15} />
      <div>{handle.props.children}</div>
    </div>
  );
}

const doneCss = css({
  display: 'flex',
  gap: t.s2,
  alignItems: 'flex-start',
  margin: '0',
  padding: '8px 12px',
  borderRadius: t.rMd,
  background: t.successSoft,
  color: t.text,
  fontSize: t.textMd,
  overflowWrap: 'anywhere',
});

/** Something that just happened (role="status"). */
export function Done(handle: Handle<{ testId?: string; children?: RemixNode }>) {
  return () => (
    <p mix={doneCss} role="status" data-testid={handle.props.testId}>
      {handle.props.children}
    </p>
  );
}

/** A table inside a quiet bordered surface, with a fixed height that scrolls when long. */
export const tableWrap = css({
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'auto',
  maxHeight: '420px',
  '& tr:last-child td': { borderBottom: '0' },
  '& thead th': { position: 'sticky', top: '0', background: t.bgSurface, zIndex: '1' },
});

/** A small sub-heading inside a group body. */
export const subhead = css({ margin: '0', fontSize: t.textSm, fontWeight: '600', color: t.textMuted, textTransform: 'uppercase', letterSpacing: '0.04em' });

/** Plain bullet list in muted text. */
export const plainList = css({ margin: '0', paddingLeft: '18px', display: 'grid', gap: '2px', fontSize: t.textMd });

/** An inline confirmation row: the question and the buttons on one line. */
export const confirmRow = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  '& > span': { flex: '1 1 260px', fontSize: t.textMd },
});

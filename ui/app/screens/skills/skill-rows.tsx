// Skills as compact lists (the owner's requests, 2026-10-14 and 2026-10-16: tiles with whole descriptions were too big to read;
// a project's skills should be "a 1 line item ... and if we want to read more we can click on it and it will expand"). Two
// lists share one look: `SkillRows` is the Library's, one row per skill with a checkbox to pick it, its name, its first sentence
// and its state; clicking the name opens the row to the whole description, the license, the roles and paths it names, the
// pinned commit and why it is blocked, and several picked rows are added in one go by the page that holds the list.
// `ProjectSkillRows` is a project's, one row per skill (logo or icon, name, first sentence, state badge); the whole row is the
// button and opens in place to the skill's full panel (the page's `SkillPanel`). In both, one row is open at a time.

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { ChevronDown, ChevronRight, FileCode2 } from 'lucide';
import type { LibrarySkill, Skill } from '../../api/extTypes.ts';
import { BrandLogo, brandKey } from '../../ui/brand-logo.tsx';
import { Badge, kv } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { t, type Tone } from '../../ui/tokens.ts';
import type { TileTone } from '../../ui/tiles.tsx';
import { listOr, shortDescription, skillBrand, skillStateText } from './skills.ts';

const iconWrap = css({
  display: 'inline-grid',
  placeItems: 'center',
  flex: 'none',
  width: '32px',
  height: '32px',
  borderRadius: '7px',
  background: t.bgApp,
  color: t.textMuted,
  boxShadow: `inset 0 0 0 1px ${t.border}`,
});

/** The logo of a skill or registry: its brand's mark, or a neutral square with an icon when the brand has none. */
export function SkillLogo(handle: Handle<{ brand: string; icon?: IconNode }>) {
  return () => {
    const { brand, icon = FileCode2 } = handle.props;
    return brand && brandKey(brand) ? (
      <BrandLogo id={brand} size={32} />
    ) : (
      <span mix={iconWrap} aria-hidden="true">
        <Icon icon={icon} size={16} />
      </span>
    );
  };
}

const listCss = css({
  listStyle: 'none',
  margin: '0',
  padding: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '& > li + li': { borderTop: `1px solid ${t.border}` },
});
const rowCss = css({
  display: 'grid',
  gridTemplateColumns: 'auto minmax(0, 1fr) auto',
  alignItems: 'center',
  gap: t.s3,
  padding: `${t.s2} ${t.s3}`,
  minHeight: '44px',
  '& input[type="checkbox"]': { width: '16px', height: '16px', margin: '0' },
  '& .what': {
    display: 'flex',
    alignItems: 'baseline',
    gap: t.s2,
    minWidth: '0',
    background: 'none',
    border: '0',
    padding: '0',
    color: 'inherit',
    font: 'inherit',
    textAlign: 'left',
    cursor: 'pointer',
  },
  '& .what:focus-visible': { boxShadow: t.focus, outline: 'none', borderRadius: t.rSm },
  '& .name': { fontFamily: t.mono, fontWeight: '600', fontSize: t.textMd, whiteSpace: 'nowrap' },
  '& .short': { color: t.textMuted, fontSize: t.textSm, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: '0' },
  '& .state': { display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap', justifyContent: 'flex-end' },
});
const detailCss = css({
  display: 'grid',
  gap: t.s3,
  padding: `0 ${t.s3} ${t.s3} calc(${t.s3} + 16px + ${t.s3})`,
  fontSize: t.textMd,
  '& p': { margin: '0' },
  '& .actions': { display: 'flex', gap: t.s2, flexWrap: 'wrap' },
});

// A project skill's row: the whole row is one button (the app stylesheet centres a button's content and forbids wrapping, so
// both are reset here), the logo, the name with its first sentence, the state badges and a chevron.
const projectRowCss = css({
  display: 'grid',
  gridTemplateColumns: 'auto minmax(0, 1fr) auto auto',
  alignItems: 'center',
  justifyContent: 'start',
  gap: t.s3,
  width: '100%',
  minHeight: '52px',
  padding: `${t.s2} ${t.s3}`,
  background: 'none',
  border: '0',
  borderRadius: t.rLg,
  color: 'inherit',
  font: 'inherit',
  textAlign: 'left',
  whiteSpace: 'normal',
  cursor: 'pointer',
  '&:hover': { background: t.bgHover },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '&[aria-expanded="true"]': { borderBottomLeftRadius: '0', borderBottomRightRadius: '0' },
  '& .text': { display: 'flex', alignItems: 'baseline', gap: t.s2, minWidth: '0' },
  '& .name': { fontFamily: t.mono, fontWeight: '600', fontSize: t.textMd, whiteSpace: 'nowrap' },
  '& .short': { color: t.textMuted, fontSize: t.textSm, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: '0' },
  '& .state': { display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap', justifyContent: 'flex-end' },
  '& .chev': { display: 'inline-flex', color: t.textMuted },
});
const projectDetailCss = css({
  padding: `0 ${t.s3} ${t.s3} calc(${t.s3} + 32px + ${t.s3})`,
  '@media (max-width: 640px)': { paddingLeft: t.s3 },
});

const badgeTone = (tone: TileTone): Tone => (tone === 'bad' ? 'err' : tone === '' ? 'muted' : tone);

export interface ProjectSkillRowsProps {
  skills: readonly Skill[];
  /** Names of the project skills whose text is newer in the Library. */
  updates: ReadonlySet<string>;
  /** The open row, by skill name; '' when none. */
  open: string;
  onToggle: (name: string) => void;
  /** The open row's content: the skill's full panel. */
  panel: (s: Skill) => RemixNode;
  label: string;
  testId: string;
}

/** The project's skills as one-line rows that open in place; the data attributes tests and styles read sit on the row (`li`). */
export function ProjectSkillRows(handle: Handle<ProjectSkillRowsProps>) {
  return () => {
    const { skills, updates, open, onToggle, panel, label, testId } = handle.props;
    return (
      <ul mix={listCss} aria-label={label}>
        {skills.map((sk) => {
          const hasUpdate = updates.has(sk.name);
          const state = skillStateText(sk, hasUpdate);
          const isOpen = open === sk.name;
          const detailId = `skill-panel-${sk.id}`;
          const short = shortDescription(sk.description || (sk.origin === 'registry' && sk.registryId ? `From ${sk.registryId}.` : 'From this repository.'));
          return (
            <li key={sk.id} data-testid={testId} data-name={sk.name} data-status={sk.status} data-check={String(sk.status === 'accepted')} data-update={String(hasUpdate)}>
              <button type="button" class="what" mix={[projectRowCss, on('click', () => onToggle(sk.name))]} aria-expanded={isOpen ? 'true' : 'false'} aria-controls={detailId}>
                <SkillLogo brand={skillBrand(sk.registryId, sk.origin)} />
                <span class="text">
                  <span class="name">{sk.name}</span>
                  <span class="short">{short}</span>
                </span>
                <span class="state">
                  <Badge tone={badgeTone(state.tone)} testId="skill-state">
                    {state.text}
                  </Badge>
                  {sk.hasScripts ? <Badge tone="warn">has scripts</Badge> : null}
                </span>
                <span class="chev" aria-hidden="true">
                  <Icon icon={isOpen ? ChevronDown : ChevronRight} size={14} />
                </span>
              </button>
              {isOpen ? (
                <div mix={projectDetailCss} id={detailId} data-testid={`${testId}-detail`}>
                  {panel(sk)}
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
    );
  };
}

export interface SkillRowsProps {
  skills: readonly LibrarySkill[];
  /** Names (registryId/name) of the rows picked. */
  picked: ReadonlySet<string>;
  onPick: (key: string, on: boolean) => void;
  /** Names already in the chosen project: shown with a check mark and not pickable. */
  have: ReadonlySet<string>;
  /** registryId/name of what the last sync brought. */
  fresh?: ReadonlySet<string>;
  projectName: string;
  /** Extra controls in an open row (the page's own Add button). */
  actions?: (l: LibrarySkill) => RemixNode;
  label: string;
  testId: string;
}

/** The key a picked row is known by: one name may come from several registries. */
export const pickKey = (l: Pick<LibrarySkill, 'registryId' | 'name'>): string => `${l.registryId}/${l.name}`;

export function SkillRows(handle: Handle<SkillRowsProps>) {
  let open = '';
  const toggle = (k: string) => {
    open = open === k ? '' : k;
    void handle.update();
  };
  return () => {
    const { skills, picked, onPick, have, fresh, projectName, actions, label, testId } = handle.props;
    return (
      <ul mix={listCss} aria-label={label}>
        {skills.map((l) => {
          const k = pickKey(l);
          const inProject = have.has(l.name);
          const blocked = l.status === 'blocked';
          const isOpen = open === k;
          const detailId = `skill-detail-${l.id}`;
          return (
            <li key={k} data-testid={testId} data-name={l.name} data-registry={l.registryId} data-status={l.status} data-check={String(inProject)}>
              <div mix={rowCss}>
                <input
                  type="checkbox"
                  aria-label={`Pick ${l.name}`}
                  checked={picked.has(k)}
                  disabled={blocked || inProject}
                  title={blocked ? 'Blocked: it cannot be added' : inProject ? `${projectName} already has it` : undefined}
                  mix={on('change', (e) => onPick(k, e.currentTarget.checked))}
                />
                <button type="button" class="what" aria-expanded={isOpen ? 'true' : 'false'} aria-controls={detailId} mix={on('click', () => toggle(k))}>
                  <Icon icon={isOpen ? ChevronDown : ChevronRight} size={14} />
                  <span class="name">{l.name}</span>
                  <span class="short">{shortDescription(l.description || 'No description.')}</span>
                </button>
                <span class="state">
                  {fresh?.has(k) ? <Badge tone="info">new</Badge> : null}
                  {inProject ? <Badge tone="ok">In {projectName} ✓</Badge> : null}
                  {blocked ? <Badge tone="err">blocked</Badge> : null}
                  {l.gone ? <Badge tone="warn">no longer upstream</Badge> : null}
                </span>
              </div>
              {isOpen ? (
                <div mix={detailCss} id={detailId} data-testid={`${testId}-detail`}>
                  <p>{l.description || 'No description.'}</p>
                  <dl mix={kv}>
                    <dt>From</dt>
                    <dd>{l.repo}</dd>
                    <dt>License</dt>
                    <dd>{l.license || 'not stated'}</dd>
                    <dt>Roles it names</dt>
                    <dd>{listOr(l.roles)}</dd>
                    <dt>Paths</dt>
                    <dd>{listOr(l.globs)}</dd>
                    <dt>Pinned commit</dt>
                    <dd>
                      <code>{l.pinnedCommit.slice(0, 12)}</code>
                    </dd>
                  </dl>
                  {l.hasScripts ? <Notice tone="warn">The skill directory contains scripts. Drydock never runs them.</Notice> : null}
                  {(l.flags ?? []).length > 0 ? <Notice tone="warn">Flags: {(l.flags ?? []).join(', ')}</Notice> : null}
                  {blocked ? <ErrorNote error={`Blocked: ${l.blockReason || 'the core did not say why'}. A blocked skill cannot be added.`} /> : null}
                  {!blocked && actions ? <div class="actions">{actions(l)}</div> : null}
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
    );
  };
}

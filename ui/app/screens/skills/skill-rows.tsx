// Library skills as a compact list (the owner's request, 2026-10-14: tiles with whole descriptions were too big to read): one
// row per skill with a checkbox to pick it, its name, its first sentence and its state; clicking the name opens the row to the
// whole description, the license, the roles and paths it names, the pinned commit and why it is blocked. Several picked rows
// are added in one go by the page that holds the list.

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { ChevronDown, ChevronRight } from 'lucide';
import type { LibrarySkill } from '../../api/extTypes.ts';
import { Badge, kv } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { listOr, shortDescription } from './skills.ts';

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

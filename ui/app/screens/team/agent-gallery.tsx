// The prebuilt agents (internal/agentcatalog) as compact cards grouped by category: what each does, its role, the skills and
// MCP connectors it comes with, and whether its preferred agent CLI is installed here. With `onPick`, each card has a
// button (the wizard adds the agent to the team list; the Team page applies it to the project); without, it is read-only
// (the Library page).

import { css, on, type Handle } from 'remix/component';
import { Plug, Plus, Sparkles } from 'lucide';
import { AGENT_CATEGORIES, type AgentCategory, type PrebuiltAgent } from '../../api/types.ts';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { CATEGORY_LABEL } from '../project/wizard.ts';

const groupCss = css({
  display: 'grid',
  gap: t.s2,
  '& + &': { marginTop: t.s4 },
  '& h3': { margin: '0', fontSize: t.textXs, fontWeight: '600', color: t.textFaint, letterSpacing: '0.04em', textTransform: 'uppercase' },
});
const gridCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(250px, 1fr))', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });
const cardCss = css({
  display: 'grid',
  gridTemplateRows: 'auto 1fr auto',
  gap: '6px',
  padding: `${t.s3} ${t.s3} 10px`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  transition: `border-color ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong },
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
  '& h4': { margin: '0', fontSize: t.textMd, fontWeight: '600', flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .sum': {
    margin: '0',
    color: t.textMuted,
    fontSize: t.textSm,
    lineHeight: '1.45',
    display: '-webkit-box',
    WebkitLineClamp: '2',
    WebkitBoxOrient: 'vertical',
    overflow: 'hidden',
  },
  '& .foot': { display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap', minWidth: '0', fontSize: t.textXs, color: t.textMuted },
  '& .chip': { display: 'inline-flex', alignItems: 'center', gap: '3px', height: '18px', padding: '0 6px', border: `1px solid ${t.border}`, borderRadius: t.rSm },
  '& .cli': { display: 'inline-flex', alignItems: 'center', gap: '4px' },
  '& .dot': { width: '6px', height: '6px', borderRadius: '50%' },
  '& .act': { marginLeft: 'auto' },
});

/** The entries grouped in the catalog's category order (unknown categories last, in the order they come). */
export function byCategory(entries: readonly PrebuiltAgent[]): [string, PrebuiltAgent[]][] {
  const cats = [...AGENT_CATEGORIES, ...new Set(entries.map((e) => e.category).filter((c) => !AGENT_CATEGORIES.includes(c as AgentCategory)))];
  return cats.map((c): [string, PrebuiltAgent[]] => [c, entries.filter((e) => e.category === c)]).filter(([, list]) => list.length > 0);
}

export function AgentGallery(handle: Handle<{ entries: PrebuiltAgent[]; onPick?: (e: PrebuiltAgent) => void; pickLabel?: string; busy?: string; disabled?: boolean; label?: string }>) {
  return () => {
    const { entries, onPick, pickLabel = 'Add', busy, disabled, label = 'Prebuilt agents' } = handle.props;
    return (
      <div aria-label={label} role="region" data-testid="agent-gallery">
        {byCategory(entries).map(([cat, list]) => (
          <section key={cat} mix={groupCss} aria-label={CATEGORY_LABEL[cat as AgentCategory] ?? cat}>
            <h3>{CATEGORY_LABEL[cat as AgentCategory] ?? cat}</h3>
            <ul mix={gridCss}>
              {list.map((e) => {
                const pref = e.availability.adapters.find((a) => a.preferred) ?? e.availability.adapters[0];
                const ok = e.availability.preferredUsable;
                const cli = [e.preferred.adapter, e.preferred.model].filter(Boolean).join(' · ');
                return (
                  <li key={e.id} mix={cardCss} data-testid="prebuilt" data-id={e.id}>
                    <div class="head">
                      <h4 title={e.name}>{e.name}</h4>
                      <Badge tone="muted">{e.role}</Badge>
                    </div>
                    <p class="sum" title={e.description || e.summary}>
                      {e.summary}
                    </p>
                    <div class="foot">
                      <span class="cli" title={ok ? `${e.preferred.adapter} is installed` : pref?.reason || `${e.preferred.adapter} is not installed`}>
                        <span class="dot" aria-hidden="true" style={{ background: ok ? t.success : t.warning }} />
                        {cli}
                        <span class="sr-only">{ok ? ' (installed)' : ' (not installed)'}</span>
                      </span>
                      {e.skills.length > 0 ? (
                        <span class="chip" title={e.skills.map((x) => x.name).join(', ')}>
                          <Icon icon={Sparkles} size={10} />
                          {e.skills.length} {e.skills.length === 1 ? 'skill' : 'skills'}
                        </span>
                      ) : null}
                      {e.mcp.map((m) => (
                        <span key={m} class="chip" title={`MCP connector ${m}`}>
                          <Icon icon={Plug} size={10} />
                          {m}
                        </span>
                      ))}
                      {onPick ? (
                        <span class="act">
                          <Button size="sm" variant="secondary" icon={Plus} disabled={disabled || !!busy} aria-label={`${pickLabel} ${e.name}`} mix={on('click', () => onPick(e))}>
                            {busy === e.id ? 'Adding...' : pickLabel}
                          </Button>
                        </span>
                      ) : null}
                    </div>
                  </li>
                );
              })}
            </ul>
          </section>
        ))}
      </div>
    );
  };
}

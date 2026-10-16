// The prebuilt agents (internal/agentcatalog) as compact cards grouped by category: what each does, its role, the skills and
// MCP connectors it comes with (by their full names, never a bare id), and whether its preferred agent CLI is installed here.
// Every card opens a details view (decision P43): the agent's model and CLI, each skill with what it does (its SKILL.md
// description at the pin), its source repository and pinned commit and its license, and each MCP server with its logo,
// publisher and what it gives agents. With `onPick`, each card has an Add button (the wizard adds the agent to the team
// list; the Team page applies it to the project); without, it only shows (the Library page).

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, Info, Plug, Plus, Sparkles } from 'lucide';
import { AGENT_CATEGORIES, type AgentCategory, type PrebuiltAgent } from '../../api/types.ts';
import { BrandLogo } from '../../ui/brand-logo.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Icon } from '../../ui/icon.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { CATEGORY_LABEL } from '../project/wizard.ts';
import { SkipsBadge } from '../shared/skip-approvals.tsx';

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
  '& h4 button': { all: 'unset', cursor: 'pointer', maxWidth: '100%', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& h4 button:hover': { textDecoration: 'underline' },
  '& h4 button:focus-visible': { outline: `2px solid ${t.accent}`, outlineOffset: '2px', borderRadius: t.rSm },
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
  '& .chip': { display: 'inline-flex', alignItems: 'center', gap: '4px', height: '20px', padding: '0 6px', border: `1px solid ${t.border}`, borderRadius: t.rSm, background: 'transparent', color: 'inherit', font: 'inherit' },
  '& button.chip': { cursor: 'pointer' },
  '& .cli': { display: 'inline-flex', alignItems: 'center', gap: '4px' },
  '& .dot': { width: '6px', height: '6px', borderRadius: '50%' },
  '& .act': { marginLeft: 'auto', display: 'inline-flex', gap: '4px' },
});
const detailCss = css({
  display: 'grid',
  gap: t.s4,
  '& p': { margin: '0' },
  '& h3': { margin: '0 0 6px', fontSize: t.textXs, fontWeight: '600', color: t.textFaint, letterSpacing: '0.04em', textTransform: 'uppercase' },
  '& ul': { display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' },
  '& li': { display: 'grid', gap: '4px', padding: t.s2, border: `1px solid ${t.border}`, borderRadius: t.rMd },
  '& .line': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& dl': { display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: '4px 12px', margin: '0', fontSize: t.textSm },
  '& dt': { color: t.textMuted },
  '& dd': { margin: '0' },
});

/** The entries grouped in the catalog's category order (unknown categories last, in the order they come). */
export function byCategory(entries: readonly PrebuiltAgent[]): [string, PrebuiltAgent[]][] {
  const cats = [...AGENT_CATEGORIES, ...new Set(entries.map((e) => e.category).filter((c) => !AGENT_CATEGORIES.includes(c as AgentCategory)))];
  return cats.map((c): [string, PrebuiltAgent[]] => [c, entries.filter((e) => e.category === c)]).filter(([, list]) => list.length > 0);
}

/** An entry's MCP servers with their full names (the core's details, or the id when an older core sent none). */
export function serversOf(e: PrebuiltAgent): { id: string; display: string; publisher: string; brand: string; summary: string }[] {
  return e.mcp.map((id) => {
    const m = e.availability.mcp.find((x) => x.id === id);
    return { id, display: m?.display || id, publisher: m?.publisher ?? '', brand: m?.brand || id, summary: m?.summary ?? '' };
  });
}

/** An entry's skills with what each does (the core's details, or only the reference when an older core sent none). */
export function skillsOf(e: PrebuiltAgent): { name: string; registry: string; ref: string; description: string; license: string; repo: string; sourceUrl?: string }[] {
  return e.skills.map((k) => {
    const d = e.availability.skills.find((x) => x.name === k.name && x.registry === k.registry);
    return { ...k, description: d?.description ?? '', license: d?.license ?? '', repo: d?.repo ?? k.registry, sourceUrl: d?.sourceUrl };
  });
}

/** What a prebuilt agent comes with, for the details dialog. */
export function PrebuiltDetails(handle: Handle<{ entry: PrebuiltAgent }>) {
  return () => {
    const e = handle.props.entry;
    const skills = skillsOf(e);
    const servers = serversOf(e);
    const pref = e.availability.adapters.find((a) => a.preferred);
    return (
      <div mix={detailCss} data-testid="prebuilt-details">
        <p>{e.description || e.summary}</p>
        <section aria-label="Model and CLI">
          <h3>Model and CLI</h3>
          <dl>
            <dt>Agent CLI</dt>
            <dd data-testid="prebuilt-cli">
              {e.preferred.adapter}
              {pref ? (
                <span mix={faint}>
                  {pref.installed && pref.usable ? (pref.wslDistro ? ` (in WSL ${pref.wslDistro})` : ' (installed here)') : ` (${pref.reason || 'not installed here'})`}
                </span>
              ) : null}
            </dd>
            <dt>Model</dt>
            <dd data-testid="prebuilt-model">{e.preferred.model || "the CLI's default"}</dd>
            <dt>Effort</dt>
            <dd>{e.preferred.effort || "the CLI's default"}</dd>
            {e.preferred.permissionMode ? (
              <>
                <dt>Permission mode</dt>
                <dd data-testid="prebuilt-mode">
                  {e.preferred.permissionMode} <SkipsBadge mode={e.preferred.permissionMode} />
                </dd>
              </>
            ) : null}
            {e.alternatives.length ? (
              <>
                <dt>Also works with</dt>
                <dd>{e.alternatives.map((a) => [a.adapter, a.model].filter(Boolean).join(' · ')).join(', ')}</dd>
              </>
            ) : null}
          </dl>
        </section>
        <section aria-label="Skills">
          <h3>Skills ({skills.length})</h3>
          {skills.length === 0 ? <p mix={faint}>No skills.</p> : null}
          <ul>
            {skills.map((k) => {
              const href = k.sourceUrl ? safeHref(k.sourceUrl) : undefined;
              return (
                <li key={`${k.registry}/${k.name}`} data-testid="prebuilt-skill" data-skill={k.name}>
                  <span class="line">
                    <Icon icon={Sparkles} size={13} />
                    <strong>{k.name}</strong>
                    {k.license ? <Badge title="License">{k.license}</Badge> : null}
                  </span>
                  {k.description ? <span mix={muted}>{k.description}</span> : <span mix={faint}>No description recorded.</span>}
                  <span class="line" mix={faint}>
                    From {href ? (
                      <a href={href} target="_blank" rel="noopener noreferrer" data-rmx-document>
                        {k.repo} <Icon icon={ExternalLink} size={11} />
                      </a>
                    ) : (
                      k.repo
                    )}{' '}
                    pinned at <code mix={mono} title={k.ref}>{k.ref.slice(0, 12)}</code>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
        <section aria-label="MCP servers">
          <h3>MCP servers ({servers.length})</h3>
          {servers.length === 0 ? <p mix={faint}>No MCP servers.</p> : null}
          <ul>
            {servers.map((m) => (
              <li key={m.id} data-testid="prebuilt-server" data-server={m.id}>
                <span class="line">
                  <BrandLogo id={m.brand} size={20} />
                  <strong>{m.display}</strong>
                  {m.publisher ? <span mix={faint}>by {m.publisher}</span> : null}
                </span>
                {m.summary ? <span mix={muted}>{m.summary}</span> : null}
              </li>
            ))}
          </ul>
        </section>
      </div>
    );
  };
}

export function AgentGallery(handle: Handle<{ entries: PrebuiltAgent[]; onPick?: (e: PrebuiltAgent) => void; pickLabel?: string; busy?: string; disabled?: boolean; label?: string }>) {
  let open = '';
  const show = (id: string) => {
    open = id;
    void handle.update();
  };
  return () => {
    const { entries, onPick, pickLabel = 'Add', busy, disabled, label = 'Prebuilt agents' } = handle.props;
    const opened = entries.find((e) => e.id === open);
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
                const skillNames = e.skills.map((x) => x.name).join(', ');
                return (
                  <li key={e.id} mix={cardCss} data-testid="prebuilt" data-id={e.id}>
                    <div class="head">
                      <h4 title={e.name}>
                        <button type="button" aria-haspopup="dialog" mix={on('click', () => show(e.id))}>
                          {e.name}
                        </button>
                      </h4>
                      <Badge tone="muted">{e.role}</Badge>
                    </div>
                    <p class="sum" title={e.description || e.summary}>
                      {e.summary}
                    </p>
                    <div class="foot">
                      <span
                        class="cli"
                        title={ok ? (pref?.wslDistro ? `${e.preferred.adapter} is installed in WSL ${pref.wslDistro}` : `${e.preferred.adapter} is installed`) : pref?.reason || `${e.preferred.adapter} is not installed`}
                      >
                        <span class="dot" aria-hidden="true" style={{ background: ok ? t.success : t.warning }} />
                        {cli}
                        <span class="sr-only">{ok ? (pref?.wslDistro ? ` (installed in WSL ${pref.wslDistro})` : ' (installed)') : ' (not installed)'}</span>
                      </span>
                      <SkipsBadge mode={e.preferred.permissionMode} />
                      {e.skills.length > 0 ? (
                        <button
                          type="button"
                          class="chip"
                          title={`Skills: ${skillNames}`}
                          aria-label={`${e.skills.length} ${e.skills.length === 1 ? 'skill' : 'skills'}: ${skillNames}. Show details of ${e.name}`}
                          data-testid="prebuilt-skills-chip"
                          mix={on('click', () => show(e.id))}
                        >
                          <Icon icon={Sparkles} size={10} />
                          {e.skills.length} {e.skills.length === 1 ? 'skill' : 'skills'}
                        </button>
                      ) : null}
                      {serversOf(e).map((m) => (
                        <span key={m.id} class="chip" title={`MCP server ${m.display}${m.publisher ? ` by ${m.publisher}` : ''}${m.summary ? `: ${m.summary}` : ''}`} data-testid="prebuilt-server-chip">
                          {m.brand ? <BrandLogo id={m.brand} size={12} /> : <Icon icon={Plug} size={10} />}
                          {m.display}
                        </span>
                      ))}
                      <span class="act">
                        <Button size="sm" variant="ghost" icon={Info} aria-label={`What ${e.name} comes with`} mix={on('click', () => show(e.id))}>
                          Details
                        </Button>
                        {onPick ? (
                          <Button size="sm" variant="secondary" icon={Plus} disabled={disabled || !!busy} aria-label={`${pickLabel} ${e.name}`} mix={on('click', () => onPick(e))}>
                            {busy === e.id ? 'Adding...' : pickLabel}
                          </Button>
                        ) : null}
                      </span>
                    </div>
                  </li>
                );
              })}
            </ul>
          </section>
        ))}
        <Dialog open={!!opened} title={opened ? opened.name : ''} wide onClose={() => show('')} label={opened ? `${opened.name}: what it comes with` : undefined}>
          {opened ? <PrebuiltDetails entry={opened} /> : null}
          {opened && onPick ? (
            <div style={{ marginTop: 'var(--space-4)' }}>
              <Button
                variant="primary"
                icon={Plus}
                disabled={disabled || !!busy}
                mix={on('click', () => {
                  onPick(opened);
                  show('');
                })}
              >
                {pickLabel} {opened.name}
              </Button>
            </div>
          ) : null}
        </Dialog>
      </div>
    );
  };
}

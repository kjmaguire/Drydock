// One vendor's detail on the Integrations page (decisions P47, P98, P106): app sync for a Services row (service-detail.tsx), or
// for an Agent tools row the vendor's MCP servers as rows (the connector cards, screens/mcp/gallery.tsx) and the skill registries
// it publishes. A vendor with both has a row in each group (integrations.ts, agentToolVendors), so its servers are found under
// Agent tools like everyone else's.

import { css, navigate, type Handle } from 'remix/component';
import { Cpu } from 'lucide';
import type { IntegrationKindInfo, IntegrationView, SkillRegistry } from '../../api/extTypes.ts';
import { routes } from '../../routes.ts';
import { faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { DetailHeader, SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { t } from '../../ui/tokens.ts';
import { ConnectorCard } from '../mcp/gallery.tsx';
import { cardsState, serverKey, type Vendor } from './integrations.ts';
import { ServiceView, type Open } from './service-detail.tsx';

const panelCss = css({ display: 'grid', gap: t.s5, minWidth: '0' });
const regName = css({ display: 'inline-flex', alignItems: 'center', gap: '6px', color: t.text, fontWeight: '500', overflowWrap: 'anywhere' });

/**
 * The skill registries a vendor publishes, by name, license and whether this workspace has them enabled. Their skills are in
 * the skill library (Library > Skills), where a skill or a whole registry is added to a project.
 */
function VendorSkills(handle: Handle<{ v: Vendor; registries: SkillRegistry[] | undefined; loading: boolean; error?: Error }>) {
  return () => {
    const { v, registries, loading, error } = handle.props;
    const libraryLink = (
      <a href={routes.librarySkills.href()} data-testid="skills-link">
        skill library
      </a>
    );
    return (
      <SettingsGroup title="Skills from this vendor" testId="integration-skills" label="Skills from this vendor" actions={<span mix={faint}>Added to a project in the {libraryLink}</span>}>
        {error ? (
          <SettingRow label="Registries">
            <ErrorNote error={error} what="Skills" />
          </SettingRow>
        ) : null}
        {loading && !registries ? (
          <SettingRow label="Registries">
            <Loading label="Loading the registries..." />
          </SettingRow>
        ) : null}
        <div role="list" aria-label={`Skill registries of ${v.label}`}>
          {v.skillRegistries.map((id) => {
            const r = (registries ?? []).find((x) => x.id === id);
            const selective = r?.importMode === 'selective_only';
            return (
              <div key={id} role="listitem" data-testid="vendor-registry" data-registry={id}>
                <SettingRow
                  label={
                    <span mix={regName}>
                      <Icon icon={Cpu} size={13} />
                      {r?.repo ?? id}
                    </span>
                  }
                >
                  <span mix={muted}>
                    {r ? [r.license ? `license ${r.license}` : '', r.enabled ? '' : 'disabled'].filter(Boolean).join(' · ') : registries ? "not in this workspace's registry list" : ''}
                  </span>
                  {r && selective ? (
                    <span mix={faint} data-testid="vendor-selective" style={{ display: 'block' }}>
                      {r.notes ? `${r.notes}: ` : ''}pick its skills one at a time in the {libraryLink}.
                    </span>
                  ) : null}
                </SettingRow>
              </div>
            );
          })}
        </div>
      </SettingsGroup>
    );
  };
}

/** The vendor's MCP servers as one group of rows. */
function AgentTools(handle: Handle<{ v: Vendor; onChanged: () => void }>) {
  return () => {
    const { v, onChanged } = handle.props;
    return (
      <SettingsGroup
        title="Agent tools"
        testId="integration-agents"
        label="Agent tools (MCP)"
        actions={<span mix={faint}>Grant it to agents on a project's Integrations page</span>}
      >
        {v.cards.map((c) => (
          <ConnectorCard key={c.id} card={c} onChanged={onChanged} settingsHref={c.libraryId ? routes.integrations.href({ key: serverKey(c.libraryId) }) : undefined} />
        ))}
      </SettingsGroup>
    );
  };
}

const defaultOpen: Open = (href) => void navigate(href);

/**
 * The selected row's detail: app sync for a Services row (an integration kind: its header names the vendor), else the vendor's
 * agent tools (its servers as rows) and skill registries under a header of its own.
 */
export function VendorPanel(
  handle: Handle<{
    v: Vendor;
    info?: IntegrationKindInfo;
    conn?: IntegrationView;
    registries: SkillRegistry[] | undefined;
    registriesLoading: boolean;
    registriesError?: Error;
    onSyncChanged: () => void;
    onAgentsChanged: () => void;
    onKindsChanged?: () => void;
    open?: Open;
  }>,
) {
  return () => {
    const { v, info, conn, registries, registriesLoading, registriesError, onSyncChanged, onAgentsChanged, onKindsChanged, open } = handle.props;
    const st = cardsState(v.cards);
    return (
      <div mix={panelCss} data-testid="integration-panel" data-kind={v.key} role="region" aria-label={v.label}>
        {v.kind ? (
          <section aria-label="App sync" data-testid="integration-sync" mix={panelCss}>
            <ServiceView kind={v.kind} info={info} conn={conn} hasAgentTools={v.cards.length > 0} onChanged={onSyncChanged} onKindsChanged={onKindsChanged} open={open ?? defaultOpen} />
          </section>
        ) : (
          <>
            <DetailHeader
              title={v.label}
              logo={v.logo}
              status={st.bucket === 'needs' ? { text: 'Could not connect', tone: 'warn' } : st.bucket === 'on' ? { text: 'Connected', tone: 'ok' } : { text: 'Not connected', tone: 'muted' }}
              sub={v.blurb}
            />
            {v.cards.length > 0 ? <AgentTools v={v} onChanged={onAgentsChanged} /> : null}
            {v.skillRegistries.length > 0 ? <VendorSkills v={v} registries={registries} loading={registriesLoading} error={registriesError} /> : null}
          </>
        )}
      </div>
    );
  };
}

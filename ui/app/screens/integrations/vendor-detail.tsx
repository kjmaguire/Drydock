// One vendor's detail on the Integrations page (decisions P47, P98): app sync when the vendor has it (service-detail.tsx), its
// MCP servers as rows (the connector cards, screens/mcp/gallery.tsx) and the skill registries it publishes. VendorDetail reads what
// it needs itself, so the GitHub settings page shows GitHub's detail with the same component: connecting, testing and
// disconnecting have one implementation.

import { css, navigate, type Handle, type RemixNode } from 'remix/component';
import { Cpu } from 'lucide';
import type { IntegrationKindInfo, IntegrationView, SkillRegistry } from '../../api/extTypes.ts';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { DetailHeader, SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { t } from '../../ui/tokens.ts';
import { ConnectorCard } from '../mcp/gallery.tsx';
import { cardsState, KINDS, serverKey, tileState, vendors, type Vendor } from './integrations.ts';
import { ServiceView, type Open } from './service-detail.tsx';

const panelCss = css({ display: 'grid', gap: t.s5, minWidth: '0' });
const footerCss = css({ margin: '0', fontSize: t.textSm });
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

/** The vendor's MCP servers as one group of rows. `compact` (a service's detail): each server is one row, its facts closed. */
function AgentTools(handle: Handle<{ v: Vendor; compact: boolean; onChanged: () => void }>) {
  return () => {
    const { v, compact, onChanged } = handle.props;
    return (
      <SettingsGroup
        title="Agent tools"
        testId="integration-agents"
        label="Agent tools (MCP)"
        actions={<span mix={faint}>Grant it to agents on a project's Integrations page</span>}
      >
        {v.cards.map((c) => (
          <ConnectorCard key={c.id} card={c} onChanged={onChanged} compact={compact} settingsHref={c.libraryId ? routes.integrations.href({ key: serverKey(c.libraryId) }) : undefined} />
        ))}
      </SettingsGroup>
    );
  };
}

const defaultOpen: Open = (href) => void navigate(href);

/**
 * The selected vendor's detail: app sync (an integration kind: its header names the vendor), agent tools (its servers as rows)
 * and its skill registries. A vendor without app sync gets a header of its own. `footer` is a closing line.
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
    footer?: RemixNode;
  }>,
) {
  return () => {
    const { v, info, conn, registries, registriesLoading, registriesError, onSyncChanged, onAgentsChanged, onKindsChanged, open, footer } = handle.props;
    const tools = v.cards.length > 0 ? <AgentTools v={v} compact={!!v.kind} onChanged={onAgentsChanged} /> : null;
    const skills = v.skillRegistries.length > 0 ? <VendorSkills v={v} registries={registries} loading={registriesLoading} error={registriesError} /> : null;
    const st = cardsState(v.cards);
    return (
      <div mix={panelCss} data-testid="integration-panel" data-kind={v.key} role="region" aria-label={v.label}>
        {v.kind ? (
          <section aria-label="App sync" data-testid="integration-sync" mix={panelCss}>
            <ServiceView kind={v.kind} info={info} conn={conn} hasAgentTools={v.cards.length > 0} onChanged={onSyncChanged} onKindsChanged={onKindsChanged} open={open ?? defaultOpen}>
              {tools}
              {skills}
            </ServiceView>
          </section>
        ) : (
          <>
            <DetailHeader
              title={v.label}
              logo={v.logo}
              status={st.bucket === 'needs' ? { text: 'Needs you', tone: 'warn' } : st.bucket === 'on' ? { text: 'Connected', tone: 'ok' } : { text: 'Not connected', tone: 'muted' }}
              sub={v.blurb}
            />
            {tools}
            {skills}
          </>
        )}
        {footer ? <p mix={[muted, footerCss]}>{footer}</p> : null}
      </div>
    );
  };
}

/**
 * One vendor's detail on its own, reading what it needs itself (the connections, the kinds, the gallery and, when the vendor
 * publishes skill registries, those): the GitHub settings page shows GitHub's detail this way, the same component the
 * Integrations page opens.
 */
export function VendorDetail(handle: Handle<{ vendor: string }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.integrationList(), when: (e) => e.type.startsWith('integration.') });
  const kinds = query(handle, { load: () => s.client.integrationKinds(), when: (e) => e.type === 'integration.oauth_app_changed' });
  const gallery = query(handle, { load: () => s.client.mcpGallery(), when: (e) => e.type.startsWith('mcp.server') });
  const regs = query(handle, {
    load: () => s.client.skillRegistries(),
    enabled: () => (gallery.data ?? []).some((c) => c.integration === handle.props.vendor && (c.skillRegistries ?? []).length > 0),
  });
  return () => {
    const { vendor } = handle.props;
    const v = vendors(KINDS, gallery.data ?? []).find((x) => x.key === vendor);
    return (
      <div mix={panelCss} data-testid="vendor-detail" data-kind={vendor}>
        <ErrorNote error={q.error ?? kinds.error ?? gallery.error} what="Integrations" />
        {(q.loading && !q.data) || (gallery.loading && !gallery.data) ? <Loading /> : null}
        {v && q.data && gallery.data ? (
          <VendorPanel
            v={v}
            info={(kinds.data ?? []).find((k) => k.kind === vendor)}
            conn={v.kind ? tileState(q.data, v.key) : undefined}
            registries={regs.data}
            registriesLoading={regs.loading}
            registriesError={regs.error}
            onSyncChanged={q.reload}
            onAgentsChanged={gallery.reload}
            onKindsChanged={kinds.reload}
          />
        ) : null}
      </div>
    );
  };
}

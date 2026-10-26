// OpenRouter on the Integrations page (decision P103): a model provider, so Models owns it (its models, prices, key limits and
// endpoint uptime are on its page there), and the Integrations page shows it beside the other things Drydock connects to. Without
// one, the panel is the provider form filled in for OpenRouter: the key is all a person types. With one, its state and the way to its
// page in Models.

import { css, type Handle } from 'remix/component';
import type { Provider } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { buttonStyle } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { LearnMore } from '../../ui/learn-more.tsx';
import { DetailHeader, SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { t } from '../../ui/tokens.ts';
import { ProviderForm } from '../providers/provider-form.tsx';
import { kindWords, openRouterForm, openRouterProviders, providerHost, providerStatus } from '../providers/providers.ts';
import { providerRowState } from './integrations.ts';

const panelCss = css({ display: 'grid', gap: t.s5, minWidth: '0', '& p': { margin: '0' } });

export function OpenRouterPanel(handle: Handle<{ providers?: Provider[]; loading: boolean; error?: Error; onCreated: (p?: Provider) => void }>) {
  return () => {
    const { providers, loading, error, onCreated } = handle.props;
    const mine = openRouterProviders(providers ?? []);
    const state = providerRowState(mine);
    const first = mine[0];
    return (
      <section mix={panelCss} aria-label="OpenRouter" data-testid="openrouter-panel" data-set-up={String(mine.length > 0)}>
        <DetailHeader
          title="OpenRouter"
          logo="openrouter"
          status={providers ? { text: state.text, tone: state.tone === 'none' ? 'muted' : state.tone } : undefined}
          sub="Model provider · many vendors' models with one key"
          actions={
            first ? (
              <a href={routes.models.href({ key: first.id })} mix={buttonStyle('secondary', 'sm')} data-testid="openrouter-open">
                Open in Models
              </a>
            ) : undefined
          }
          testId="openrouter-head"
        />
        <ErrorNote error={error} what="Could not read the model providers" />
        {loading && !providers ? <Loading /> : null}
        {providers && mine.length === 0 ? (
          <>
            {/* one sentence (the density budget): where the key is kept, the form's own hint says */}
            <p mix={muted}>
              Drydock may call its models directly; on a private route it asks OpenRouter to collect no data and keep none.{' '}
              <LearnMore topic="providers" />
            </p>
            <ProviderForm initial={openRouterForm()} label="Connect OpenRouter" submitLabel="Connect OpenRouter" onCreated={onCreated} />
          </>
        ) : null}
        {mine.length > 0 ? (
          <>
            <SettingsGroup title={mine.length === 1 ? 'Connected' : `Connected (${mine.length})`} testId="openrouter-connected">
              {mine.map((p) => {
                const st = providerStatus(p);
                return (
                  <SettingRow
                    key={p.id}
                    label={p.name}
                    testId="openrouter-provider"
                    control={
                      <a href={routes.models.href({ key: p.id })} mix={buttonStyle('ghost', 'sm')}>
                        Open
                      </a>
                    }
                  >
                    <span data-testid="openrouter-provider-state" data-tone={st.tone}>
                      {st.text} · {kindWords(p.kind)} · {providerHost(p.baseUrl)} · {p.dataClass} · {p.tier}
                    </span>
                  </SettingRow>
                );
              })}
            </SettingsGroup>
            <p mix={muted}>Its models, prices, key limits and the endpoints Drydock skips for low uptime are on its page in Models.</p>
          </>
        ) : null}
      </section>
    );
  };
}

// Webhooks and sweeps on the workspace's Integrations page (decision P114): what runs for every connection without a person
// asking, and what a person must turn on themselves.
//
// - The webhook receiver is off until the person turns it on and chooses a port. It listens on 127.0.0.1 only: the vendors reach
//   it only through the person's own tunnel, which Drydock does not set up (the page says so in plain words, always). Per vendor:
//   the address to give the vendor (through the tunnel) and the secret the vendor signs with, kept in the credential store; Notion's
//   verification token arrives here and is used only when the person says so. Recent deliveries list what was done, never a body.
// - The weekly automatic sweep (on unless turned off) re-reads each project's whole scope once a week, the only way to notice an
//   item deleted upstream.

import { css, on, type Handle } from 'remix/component';
import { Check, KeyRound, Trash2, Webhook } from 'lucide';
import type { WebhookEndpoint } from '../../api/extTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import { parseWhole } from '../../lib/limits.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { errorAttrs, errorCss, Switch } from '../../ui/form.tsx';
import { DetailHeader, SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { t } from '../../ui/tokens.ts';
import { hookOutcomeText } from './integrations.ts';

const panelCss = css({ display: 'grid', gap: t.s5, minWidth: '0', '& p': { margin: '0' } });
const lineCss = css({ display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap', minWidth: '0' });
const listCss = css({ margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: t.s1, fontSize: t.textSm });

/** One vendor's address and signing secret. */
function Endpoint(handle: Handle<{ ep: WebhookEndpoint; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let secret = '';
  return () => {
    const { ep, onChanged } = handle.props;
    const save = async (v: string) => {
      const r = await act.run('secret', () => s.client.integrationWebhookSecret(ep.integrationId ?? '', v));
      if (r) {
        secret = '';
        onChanged();
      }
    };
    return (
      <SettingRow
        label={ep.label}
        testId="webhook-endpoint"
        control={
          ep.integrationId ? (
            <span mix={lineCss}>
              <input
                type="password"
                autocomplete="off"
                spellcheck={false}
                aria-label={`${ep.label} ${ep.secret}`}
                placeholder={ep.hasSecret ? 'Replace the ' + ep.secret : 'Paste the ' + ep.secret}
                value={secret}
                mix={on('input', (e) => ((secret = e.currentTarget.value), void handle.update()))}
              />
              <Button size="sm" icon={KeyRound} disabled={act.busy !== '' || !secret.trim()} mix={[on('click', () => void save(secret.trim()))]}>
                Save secret
              </Button>
              {ep.hasSecret ? (
                <Button size="sm" variant="ghost" icon={Trash2} disabled={act.busy !== ''} mix={[on('click', () => void save(''))]}>
                  Remove
                </Button>
              ) : null}
            </span>
          ) : undefined
        }
      >
        <span data-kind={ep.kind} data-has-secret={String(ep.hasSecret)}>
          {ep.integrationId ? (
            <>
              <code mix={mono}>{ep.url ?? ep.path}</code> · {ep.hasSecret ? `${ep.secret} saved` : `no ${ep.secret} yet: deliveries are refused`}
              <br />
              <span mix={faint}>{ep.where}</span>
            </>
          ) : (
            <span mix={muted}>Connect {ep.label} first.</span>
          )}
          <ErrorNote error={act.error} />
        </span>
      </SettingRow>
    );
  };
}

export function BackgroundPanel(handle: Handle) {
  const s = services(handle);
  const act = action(handle);
  const hooks = query(handle, { load: () => s.client.integrationWebhooks(), when: (e) => e.type.startsWith('integration.webhook') });
  const sweep = query(handle, { load: () => s.client.integrationSweep(), when: (e) => e.type === 'integration.sweep.changed' });
  let port = '';
  let portError = '';
  let seenPort = -1;
  const portId = `port-${handle.id}`;

  return () => {
    const st = hooks.data;
    if (st && st.port !== seenPort) {
      seenPort = st.port;
      port = st.port ? String(st.port) : '';
    }
    const savePort = async (enable?: boolean) => {
      const p = parseWhole(port, 1024, 'Choose a port between 1024 and 65535.');
      if (!p.ok || (p.value !== undefined && p.value > 65535)) {
        portError = p.ok ? 'Choose a port between 1024 and 65535.' : p.error;
        void handle.update();
        return;
      }
      if (enable && p.value === undefined) {
        portError = 'Choose the port first.';
        void handle.update();
        return;
      }
      portError = '';
      const r = await act.run('hooks', () => s.client.integrationWebhooksSet({ enabled: enable, port: p.value ?? 0 }));
      if (r) hooks.set(r);
    };
    const on_ = !!st?.enabled;
    return (
      <section mix={panelCss} aria-label="Webhooks and sweeps" data-testid="background-panel">
        <DetailHeader
          title="Webhooks and sweeps"
          icon={Webhook}
          status={st ? { text: st.listening ? 'Listening' : on_ ? 'Not listening' : 'Off', tone: st.listening ? 'ok' : on_ ? 'warn' : 'muted' } : undefined}
          sub="What runs for every connection: the webhook receiver (off unless you turn it on) and the weekly sweep"
          testId="background-head"
        />
        <ErrorNote error={hooks.error ?? sweep.error ?? act.error} what="Webhooks and sweeps" />
        {!st && hooks.loading ? <Loading /> : null}
        {st ? (
          <SettingsGroup title="Webhook receiver" testId="webhook-receiver">
            <Notice tone="warn" testId="webhook-expose">
              {st.exposeNote}
            </Notice>
            <SettingRow
              label="Receive webhooks"
              control={<Switch label="Receive webhooks" checked={on_} disabled={act.busy !== ''} onChange={(v) => void savePort(v)} />}
            >
              <span data-testid="webhook-state" data-listening={String(st.listening)}>
                {st.listening ? (
                  <>
                    Listening on <code mix={mono}>{st.address}</code>. A delivery only makes Drydock read the item it names, with the connection's own credential.
                  </>
                ) : on_ ? (
                  'Turned on, but not listening.'
                ) : (
                  'Off. Drydock polls each service on its own schedule.'
                )}
              </span>
            </SettingRow>
            {st.error ? <Notice tone="warn">{st.error}</Notice> : null}
            <SettingRow
              label="Port"
              control={
                <form mix={[lineCss, on<HTMLFormElement, 'submit'>('submit', (e) => (e.preventDefault(), void savePort(undefined)))]} aria-label="Webhook port">
                  <input
                    id={portId}
                    inputMode="numeric"
                    aria-label="Port on 127.0.0.1 (1024 to 65535)"
                    value={port}
                    style={{ width: '96px' }}
                    {...errorAttrs(portId, portError)}
                    mix={on('input', (e) => ((port = e.currentTarget.value), (portError = ''), void handle.update()))}
                  />
                  <Button size="sm" icon={Check} disabled={act.busy !== ''} mix={[on('click', () => void savePort(undefined))]}>
                    Save port
                  </Button>
                </form>
              }
            >
              {portError ? (
                <span id={`${portId}-error`} role="alert" mix={errorCss}>
                  {portError}
                </span>
              ) : (
                'A port of 127.0.0.1 from 1024 to 65535, chosen by you.'
              )}
            </SettingRow>
            {(st.endpoints ?? []).map((ep) => (
              <Endpoint key={ep.kind} ep={ep} onChanged={hooks.reload} />
            ))}
            {st.notionVerification ? (
              <Notice tone="info" testId="notion-verification">
                <p>
                  Notion sent a verification token at {formatDateTime(st.notionVerification.receivedAt)}: <code mix={mono}>{st.notionVerification.token}</code>
                </p>
                <p>Paste it into Notion to verify the subscription, then use it here so Drydock checks Notion's signatures with it.</p>
                <p>Anything that reaches the port could send one: use it only if you just created the subscription.</p>
                <Button
                  size="sm"
                  disabled={act.busy !== ''}
                  mix={[
                    on('click', async () => {
                      const r = await act.run('notion', () => s.client.integrationUseNotionToken());
                      if (r) hooks.set(r);
                    }),
                  ]}
                >
                  Use this token
                </Button>
              </Notice>
            ) : null}
            {(st.recent ?? []).length > 0 ? (
              <SettingRow label="Recent deliveries">
                <ul mix={listCss} data-testid="webhook-recent">
                  {(st.recent ?? []).slice(0, 8).map((d, i) => (
                    <li key={i} data-outcome={d.outcome}>
                      {formatDateTime(d.receivedAt)} · {d.kind} · {hookOutcomeText(d.outcome)}
                      {d.externalId ? <span mix={faint}> · item {d.externalId}</span> : null}
                    </li>
                  ))}
                </ul>
              </SettingRow>
            ) : null}
          </SettingsGroup>
        ) : null}
        {sweep.data ? (
          <SettingsGroup title="Weekly sweep" testId="weekly-sweep">
            <SettingRow
              label="Sweep every week"
              control={
                <Switch
                  label="Sweep every week"
                  checked={sweep.data.enabled}
                  disabled={act.busy !== ''}
                  onChange={async (v) => {
                    const r = await act.run('sweep', () => s.client.integrationSweepSet(v));
                    if (r) sweep.set(r);
                  }}
                />
              }
            >
              <span data-testid="sweep-state" data-enabled={String(sweep.data.enabled)}>
                {sweep.data.enabled
                  ? "Once a week a due sync reads each project's whole scope again and marks what is gone upstream. Full sync on a project's Integrations page does the same at any time."
                  : 'Off: an item deleted upstream stays listed until a full sync.'}
              </span>
            </SettingRow>
          </SettingsGroup>
        ) : null}
      </section>
    );
  };
}

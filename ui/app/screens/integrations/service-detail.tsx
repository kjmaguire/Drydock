// A service's detail on the Integrations page (decisions P42, P59, P98): app sync, Drydock's own connection to Linear, Notion,
// Sentry, Aikido or GitHub, connected once for the whole workspace. Not connected, it is the setup: three small steps, the way to
// connect as a segmented choice, only the field that way needs, Connect, Advanced closed, and what it reads in three lines.
// Connected, it is rows: the connection (what the API reported: account, workspace, scopes; never typed), the checks (Test, Check
// against your account), the projects it is assigned to (each opens that project's Integrations page, where the scope, items, rules
// and writes back are chosen), then what the page puts under it (the vendor's agent tools, its skills). Sync now and the … menu
// (Reconnect, Disconnect) are in the header. The secret fields are uncontrolled: read once, cleared before the request leaves.

import { css, on, ref, type Handle } from 'remix/component';
import { Blocks, Check, Ellipsis, ExternalLink, FolderOpen, FolderPlus, KeyRound, LogIn, Minus, RefreshCw, ShieldCheck, Stethoscope, Trash2, X } from 'lucide';
import type {
  ConnectIntegrationReq,
  IntegrationAuthMethod,
  IntegrationKindInfo,
  IntegrationOAuthApp,
  IntegrationOAuthSession,
  IntegrationTestReport,
  IntegrationVerifyReport,
  IntegrationView,
} from '../../api/extTypes.ts';
import { formatDateTime, plural } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { newProjectHref } from '../../shell/new.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { stack } from '../../ui/layout.tsx';
import { LearnMore } from '../../ui/learn-more.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { DetailHeader, SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { Menu } from '../../ui/menu.tsx';
import { Select } from '../../ui/select.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { connectReply, kindLabel, scopeSummary, serviceState, syncLine, syncText, verifiedText, type ServiceKind } from './integrations.ts';
import { codeSpans, sentence } from '../../ui/code-spans.tsx';

const reportCss = css({ display: 'grid', gap: t.s2, padding: t.s3, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp, '& p': { margin: '0' } });
const steps = css({ display: 'grid', gap: '4px', margin: '0', padding: '0', listStyle: 'none', fontSize: t.textMd, '& li': { display: 'flex', gap: t.s2, alignItems: 'baseline' } });
const reportList = css({ display: 'grid', gap: '4px', margin: '0', paddingLeft: t.s5, fontSize: t.textMd });
const callList = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none', fontSize: t.textSm, '& li': { display: 'flex', gap: t.s2, alignItems: 'baseline' } });
const stats = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))',
  gap: '1px',
  margin: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.border,
  overflow: 'hidden',
  '& > div': { padding: `${t.s2} ${t.s3}`, background: t.bgSurface, minWidth: '0' },
  '& dt': { fontSize: t.textXs, color: t.textMuted, fontWeight: '500' },
  '& dd': { margin: '2px 0 0', fontSize: t.textMd, overflowWrap: 'anywhere' },
});
const detailCss = css({ display: 'grid', gap: t.s5, minWidth: '0' });
const confirmCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  padding: `${t.s2} ${t.s3}`,
  borderRadius: t.rMd,
  background: t.dangerSoft,
  fontSize: t.textMd,
  '& > span': { flex: '1 1 240px' },
});
// the setup's three steps: numbered dots, the current one in the accent
const stepsCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: `${t.s2} ${t.s5}`,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  fontSize: t.textMd,
  color: t.textMuted,
  '& li': { display: 'inline-flex', alignItems: 'center', gap: t.s2 },
  '& li > span': {
    display: 'inline-grid',
    placeItems: 'center',
    width: '20px',
    height: '20px',
    borderRadius: t.rFull,
    border: `1px solid ${t.borderStrong}`,
    fontSize: t.textXs,
    fontWeight: '600',
  },
  '& li[aria-current="step"]': { color: t.text, fontWeight: '500' },
  '& li[aria-current="step"] > span': { background: t.accent, borderColor: t.accent, color: t.onAccent },
});
const formCss = css({ display: 'grid', gap: t.s4, padding: t.s4, border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface, minWidth: '0', '& p': { margin: '0' } });
// the way to connect as a segmented choice: a radio per way, drawn as one control
const methodCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '2px',
  margin: '0',
  padding: '2px',
  border: '0',
  borderRadius: t.rMd,
  background: t.bgInput,
  justifySelf: 'start',
  maxWidth: '100%',
  '& legend': { position: 'absolute', width: '1px', height: '1px', overflow: 'hidden', clip: 'rect(0 0 0 0)', whiteSpace: 'nowrap' },
  '& label': { position: 'relative', display: 'inline-flex', alignItems: 'center', height: '28px', padding: `0 ${t.s3}`, borderRadius: t.rSm, color: t.textMuted, fontSize: t.textSm, fontWeight: '500', cursor: 'pointer' },
  '& label:hover': { color: t.text },
  '& label:has(input:checked)': { background: t.bgActive, color: t.text, boxShadow: t.shadowSm },
  '& label:has(input:disabled)': { opacity: '0.6', cursor: 'not-allowed' },
  '& label:has(input:focus-visible)': { boxShadow: t.focus },
  '& input': { position: 'absolute', inset: '0', width: '100%', height: '100%', margin: '0', opacity: '0', cursor: 'inherit' },
});
const readsCss = css({ display: 'grid', gap: '6px', margin: '0', padding: '0', listStyle: 'none', '& li': { display: 'flex', gap: t.s2, alignItems: 'baseline', color: t.textMuted } });
const codeCss = css({ fontFamily: t.mono, fontSize: t.textLg, fontWeight: '600', letterSpacing: '0.08em' });
const toolbar = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });
const moreCss = css({
  display: 'inline-grid',
  placeItems: 'center',
  width: '30px',
  height: '30px',
  padding: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textMuted,
  '&:hover': { background: t.bgHover, color: t.text },
});
const oauthAppCss = css({ display: 'grid', gap: t.s3, padding: t.s3, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp, '& > p': { margin: '0', fontSize: t.textMd } });
const linkList = css({ display: 'flex', gap: t.s3, flexWrap: 'wrap', margin: '0', padding: '0', listStyle: 'none', fontSize: t.textSm });

/** The test report: each step, and what the service said the credential reaches. */
export function ReportView(handle: Handle<{ r: IntegrationTestReport }>) {
  return () => {
    const { r } = handle.props;
    return (
      <div mix={reportCss} data-testid="int-report" data-ok={String(r.ok)}>
        <p>
          <Badge tone={r.ok ? 'ok' : 'err'}>{r.ok ? 'passed' : 'did not pass'}</Badge> {r.summary}
        </p>
        <ol mix={steps}>
          {(r.steps ?? []).map((st) => (
            <li key={st.id}>
              <span style={{ color: st.ok ? tone.ok.fg : tone.err.fg, display: 'inline-flex' }}>
                <Icon icon={st.ok ? Check : X} size={14} />
              </span>
              <span>
                {st.name}: {st.ok ? 'ok' : 'failed'} <span mix={faint}>{st.detail}</span>
              </span>
            </li>
          ))}
        </ol>
      </div>
    );
  };
}

/**
 * The report of Check against your account (decision P59): the verdict, what was read (counts only: the core never sends item
 * text, names or the credential), what did not match what Drydock expects, each step, and the calls made with their status codes.
 */
export function VerifyReportView(handle: Handle<{ r: IntegrationVerifyReport }>) {
  return () => {
    const { r } = handle.props;
    const read = r.read;
    const mismatches = r.mismatches ?? [];
    const notes = r.notes ?? [];
    const calls = r.calls ?? [];
    const yes = (b: boolean) => (b ? 'yes' : 'no');
    // what was read means something only once the vendor accepted the credential
    const reached = (r.steps ?? []).some((st) => st.id === 'identity' && st.ok);
    return (
      <div mix={reportCss} data-testid="verify-report" data-ok={String(r.ok)}>
        <p>
          <Badge tone={r.ok ? 'ok' : 'err'}>{r.ok ? 'verified' : 'did not pass'}</Badge> {r.summary}
        </p>
        {reached ? (
          <dl mix={stats} data-testid="verify-read" aria-label="What was read">
            <div>
              <dt>Account named</dt>
              <dd>{yes(read.accountNamed)}</dd>
            </div>
            <div>
              <dt>Workspace named</dt>
              <dd>{yes(read.workspaceNamed)}</dd>
            </div>
            <div>
              <dt>Choices listed</dt>
              <dd>{read.choices}</dd>
            </div>
            <div>
              <dt>Items read</dt>
              <dd>
                {read.items}
                {read.morePages ? ' (more not read)' : ''}
              </dd>
            </div>
            <div>
              <dt>Full text read</dt>
              <dd>{read.detailRead ? 'yes, not kept' : 'no'}</dd>
            </div>
            <div>
              <dt>Calls</dt>
              <dd>{calls.length === 0 ? 'through gh' : `${calls.filter((c) => c.ok).length} of ${calls.length} answered`}</dd>
            </div>
          </dl>
        ) : null}
        {mismatches.length > 0 ? (
          <div>
            <p>
              <strong>Did not match what Drydock expects</strong>
            </p>
            <ul mix={reportList}>
              {mismatches.map((m) => (
                <li key={m} data-testid="verify-mismatch">
                  {m}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        <ol mix={steps}>
          {(r.steps ?? []).map((st) => (
            <li key={st.id} data-testid="verify-step" data-ok={String(st.ok)}>
              <span style={{ color: st.skipped ? tone.muted.fg : st.ok ? tone.ok.fg : tone.err.fg, display: 'inline-flex' }}>
                <Icon icon={st.skipped ? Minus : st.ok ? Check : X} size={14} />
              </span>
              <span>
                {st.name}: {st.skipped ? 'skipped' : st.ok ? 'ok' : 'failed'} <span mix={faint}>{st.detail}</span>
              </span>
            </li>
          ))}
        </ol>
        {notes.map((n) => (
          <p key={n} mix={faint}>
            {n}
          </p>
        ))}
        {calls.length > 0 ? (
          <Disclosure summary={`Calls made (${calls.length})`} wide>
            <ul mix={callList} data-testid="verify-calls">
              {calls.map((c, i) => (
                <li key={`${i}-${c.endpoint}`} data-testid="verify-call" data-status={String(c.status)}>
                  <Badge tone={c.ok ? 'ok' : 'err'}>{c.status ? `HTTP ${c.status}` : 'no answer'}</Badge>
                  <span mix={mono}>{c.endpoint}</span>
                </li>
              ))}
            </ul>
          </Disclosure>
        ) : null}
      </div>
    );
  };
}

/**
 * A sign-in needs an OAuth app registered with the vendor (the vendors' own APIs have no dynamic client registration, unlike
 * their MCP servers): where to register one, and the public client id to paste. Saving it turns the sign-in on; an empty id turns
 * it off again. A client id is not a secret, so it is shown; it is never logged.
 */
function OAuthAppSetup(handle: Handle<{ kind: string; label: string; app: IntegrationOAuthApp; onSaved: () => void; onCancel?: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let clientId = handle.props.app.clientId ?? '';
  let baseUrl = handle.props.app.baseUrl ?? '';
  const upd = (f: () => void) => {
    f();
    void handle.update();
  };
  return () => {
    const { kind, label, app, onSaved, onCancel } = handle.props;
    const href = safeHref(app.registerUrl);
    const save = async (e: SubmitEvent) => {
      e.preventDefault();
      const r = await act.run('save', () => s.client.integrationOAuthAppSet(kind, clientId.trim(), app.baseUrlSetting ? baseUrl.trim() || undefined : undefined));
      if (r) onSaved();
    };
    return (
      <form mix={[oauthAppCss, on<HTMLFormElement, 'submit'>('submit', save)]} data-testid="oauth-app-setup" aria-label={`${label} OAuth app`} autocomplete="off">
        <p>
          Sign in needs an OAuth app: register one{' '}
          {href ? (
            <a href={href} target="_blank" rel="noreferrer noopener" data-rmx-document data-testid="oauth-app-register">
              at {label}
            </a>
          ) : (
            `with ${label}`
          )}{' '}
          and paste its client id here. A client id is not a secret; the app needs no client secret.
          {app.redirectUri ? ` Allow the redirect URL ${app.redirectUri}.` : ' Turn on its device flow.'}
          {(app.scopes ?? []).length > 0 ? ` Scopes: ${(app.scopes ?? []).join(' ')}.` : ''}
        </p>
        <ErrorNote error={act.error} what="Not saved" />
        <Field label="Client ID" hint={app.source === 'build' ? 'This build carries one already; yours replaces it.' : undefined}>
          {(id) => <input id={id} value={clientId} spellcheck={false} autocomplete="off" mix={on('input', (e) => upd(() => (clientId = e.currentTarget.value)))} />}
        </Field>
        {app.baseUrlSetting ? (
          <Field label={`${label} URL`} hint={`Only for a self-hosted ${label} the app is registered with; empty for the vendor's own.`}>
            {(id) => <input id={id} value={baseUrl} spellcheck={false} mix={on('input', (e) => upd(() => (baseUrl = e.currentTarget.value)))} />}
          </Field>
        ) : null}
        <div mix={formActions}>
          <Button variant="primary" type="submit" icon={KeyRound} disabled={act.busy !== ''}>
            {act.busy === 'save' ? 'Saving...' : clientId.trim() ? 'Save the client ID' : app.clientId ? 'Remove the client ID' : 'Save the client ID'}
          </Button>
          {onCancel ? (
            <Button variant="ghost" mix={[on('click', onCancel)]}>
              Cancel
            </Button>
          ) : null}
        </div>
      </form>
    );
  };
}

/** Connect (or reconnect) one service: the way to connect, the fields that way needs, Advanced closed. */
export function ConnectForm(
  handle: Handle<{
    kind: string;
    info?: IntegrationKindInfo;
    reconnectId?: string;
    onDone: (r: { test?: IntegrationTestReport }) => void;
    onKindsChanged?: () => void;
    /** The page above already says whether gh is installed and signed in (the GitHub page's hero): the form does not say it again. */
    ghStateAbove?: boolean;
  }>,
) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let editingApp = false; // the OAuth app's client id is being changed although the sign-in is on
  const secrets: Record<string, HTMLInputElement | undefined> = {};
  let method = '';
  let values: Record<string, string> = {};
  let oauth: IntegrationOAuthSession | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let pollGen = 0; // a poll answers only for the sign-in it was started for
  const gh = query(handle, { load: () => s.client.githubStatus(), enabled: () => handle.props.kind === 'github' });
  const stopPoll = () => {
    pollGen++;
    clearTimeout(timer);
    timer = undefined;
  };
  handle.signal.addEventListener('abort', stopPoll);
  const upd = (f: () => void) => {
    f();
    void handle.update();
  };
  const finish = async (req: Omit<ConnectIntegrationReq, 'kind' | 'id'>) => {
    // the reply is checked in the action, so a wrong shape is the page's error note and not a crash in the report view
    const r = await act.run('connect', async () => connectReply(await s.client.integrationConnect({ kind: handle.props.kind, id: handle.props.reconnectId, ...req })));
    if (r) handle.props.onDone(r);
  };
  // while a sign-in is open, ask the core how it went; once signed in, connect with it. One question at a time: the next is
  // asked a second after the last answer, so a slow answer never overlaps another and the sign-in connects once.
  const startPoll = () => {
    stopPoll();
    const gen = pollGen;
    const tick = async () => {
      timer = undefined;
      if (gen !== pollGen || !oauth || oauth.state !== 'waiting') return;
      try {
        const st = await s.client.integrationOAuthStatus(oauth.id);
        if (gen !== pollGen) return;
        if (st.state !== 'waiting') {
          stopPoll();
          oauth = st;
          void handle.update();
          if (st.state === 'signed_in') await finish({ oauthSession: st.id, baseUrl: (values.baseUrl ?? '').trim() || undefined });
          return;
        }
      } catch {
        if (gen !== pollGen) return; // the next poll tries again
      }
      timer = setTimeout(tick, 1000);
    };
    timer = setTimeout(tick, 1000);
  };

  return () => {
    const { kind, info, reconnectId, onKindsChanged } = handle.props;
    const ro = s.workspace.license;
    const label = info?.label ?? kindLabel(kind);
    const methods: IntegrationAuthMethod[] = info?.auth ?? [];
    const cur = methods.find((m) => m.id === method) ?? methods.find((m) => m.available) ?? methods[0];
    const ghNotReady = kind === 'github' && cur?.id === 'gh' && !reconnectId && !!gh.data && !gh.data.loggedIn;
    // an OAuth method that is off can still be chosen when an app can be registered for it: it then shows the registration
    const pickable = (m: IntegrationAuthMethod) => m.available || (m.id === 'oauth' && !!m.oauthApp);
    const fields = cur?.fields ?? [];
    const plain = fields.filter((f) => !f.advanced);
    const advanced = fields.filter((f) => f.advanced);
    const signHref = oauth?.url ? safeHref(oauth.url) : undefined;
    const submit = async (e: SubmitEvent) => {
      e.preventDefault();
      if (!cur || cur.id === 'oauth') return;
      const req: Record<string, unknown> = {}; // the fields of this service's form: token, clientId, region, config values...
      for (const f of fields) {
        if (f.secret) {
          const el = secrets[f.key];
          req[f.key] = el?.value || undefined;
          if (el) el.value = '';
        } else {
          const v = (values[f.key] ?? f.options?.[0]?.value ?? '').trim();
          req[f.key] = v || undefined;
        }
      }
      await finish(req as Omit<ConnectIntegrationReq, 'kind' | 'id'>);
    };
    const renderField = (f: NonNullable<IntegrationAuthMethod['fields']>[number]) => (
      <Field key={`${kind}-${cur?.id}-${f.key}`} label={f.label} hint={f.hint}>
        {(id) =>
          f.secret ? (
            <input id={id} type="password" autocomplete="new-password" spellcheck={false} name={`integration-${f.key}`} aria-label={f.label} mix={ref((n: HTMLInputElement) => (secrets[f.key] = n))} />
          ) : f.options && f.options.length > 0 ? (
            <Select
              id={id}
              label={f.label}
              value={values[f.key] ?? f.options[0]!.value}
              options={f.options.map((o) => ({ value: o.value, label: o.label }))}
              onChange={(v) => upd(() => (values = { ...values, [f.key]: v }))}
            />
          ) : (
            <input id={id} value={values[f.key] ?? ''} spellcheck={false} mix={on('input', (e) => upd(() => (values = { ...values, [f.key]: e.currentTarget.value })))} />
          )
        }
      </Field>
    );
    return (
      <form mix={[formCss, on<HTMLFormElement, 'submit'>('submit', submit)]} autocomplete="off" aria-label={reconnectId ? `Reconnect ${label}` : `Connect ${label}`} data-testid="connect-form">
        <ErrorNote error={act.error} what="Not connected" />
        {methods.length > 1 ? (
          <fieldset mix={methodCss}>
            <legend>How to connect</legend>
            {methods.map((m) => (
              <label key={m.id} title={m.note}>
                <input type="radio" name={`int-method-${kind}-${handle.id}`} value={m.id} checked={cur?.id === m.id} disabled={!pickable(m)} mix={on('change', () => upd(() => (method = m.id)))} />
                {m.label}
              </label>
            ))}
          </fieldset>
        ) : null}
        {cur?.note ? <p mix={faint}>{codeSpans(cur.note)}</p> : null}
        {info?.noSignIn ? (
          <p mix={faint} data-testid="integration-no-signin">
            {info.noSignIn}
          </p>
        ) : null}
        {kind === 'github' && cur?.id === 'gh' && !handle.props.ghStateAbove ? (
          <div data-testid="gh-status">
            {gh.loading && !gh.data ? <Loading label="Checking gh" /> : null}
            {gh.data?.loggedIn ? (
              <Notice tone="ok">gh is signed in as {gh.data.login}. A connection made this way uses that sign-in and stores no GitHub token.</Notice>
            ) : gh.data ? (
              <Notice tone="warn">{codeSpans(sentence(gh.data.error || 'gh is not signed in'))}. The Agents page shows the GitHub CLI.</Notice>
            ) : null}
          </div>
        ) : null}
        {cur?.id === 'oauth' && cur.oauthApp && (!cur.available || editingApp) ? (
          <OAuthAppSetup
            kind={kind}
            label={label}
            app={cur.oauthApp}
            onSaved={() => {
              editingApp = false;
              onKindsChanged?.();
              void handle.update();
            }}
            onCancel={editingApp ? () => upd(() => (editingApp = false)) : undefined}
          />
        ) : cur?.id === 'oauth' ? (
          <div mix={stack}>
            {cur.oauthApp?.clientId ? (
              <p mix={faint} data-testid="oauth-app-in-use">
                Signs in with the OAuth app {cur.oauthApp.source === 'build' ? 'this build carries' : 'you registered'} (client id {cur.oauthApp.clientId}).{' '}
                <Button variant="link" size="sm" mix={[on('click', () => upd(() => (editingApp = true)))]}>
                  Change it
                </Button>
              </p>
            ) : null}
            {oauth?.state === 'failed' ? <ErrorNote error={new Error(oauth.error || 'the sign-in failed')} what={`Could not sign in to ${label}`} /> : null}
            {oauth?.userCode && oauth.state === 'waiting' ? (
              <Notice tone="info" title="Enter this code on the sign-in page">
                <span mix={codeCss} data-testid="oauth-user-code">
                  {oauth.userCode}
                </span>
              </Notice>
            ) : null}
            <div mix={toolbar}>
              <Button
                variant="primary"
                icon={LogIn}
                disabled={act.busy !== '' || ro.readOnly || !cur.available}
                mix={[
                  on('click', async () => {
                    const r = await act.run('oauth', () => s.client.integrationOAuthStart(kind, (values.baseUrl ?? '').trim() || undefined));
                    if (r) {
                      oauth = r;
                      void handle.update();
                      startPoll();
                    }
                  }),
                ]}
              >
                {cur.label}
              </Button>
              {signHref && oauth?.state === 'waiting' ? (
                <a href={signHref} target="_blank" rel="noreferrer noopener" data-rmx-document data-testid="integration-oauth-link" mix={buttonStyle('link', 'sm')}>
                  <Icon icon={ExternalLink} size={13} />
                  Open the {label} sign-in page
                </a>
              ) : null}
            </div>
          </div>
        ) : (
          plain.map(renderField)
        )}
        <ReadOnlyNote />
        {cur?.id !== 'oauth' ? (
          <div mix={formActions}>
            {/* the gh sign-in cannot connect while gh is missing or signed out: the button waits for it (UI audit 2026-10-26) */}
            <Button
              variant="primary"
              type="submit"
              icon={kind === 'github' ? Blocks : KeyRound}
              disabled={act.busy !== '' || ro.readOnly || !cur || ghNotReady}
              title={ro.readOnly ? ro.reason : ghNotReady ? 'Install gh and sign in with gh auth login first' : undefined}
            >
              {reconnectId ? `Reconnect ${label}` : kind === 'github' ? 'Use the GitHub CLI sign-in' : `Connect ${label}`}
            </Button>
          </div>
        ) : null}
        {advanced.length > 0 ? (
          <Disclosure summary="Advanced">
            <div mix={stack}>{advanced.map(renderField)}</div>
          </Disclosure>
        ) : null}
      </form>
    );
  };
}

/** The vendor's API in a few facts (closed): which API, its version and SDK, the rate limit, the calls a sync makes, the docs. */
function ApiFacts(handle: Handle<{ label: string; info?: IntegrationKindInfo; conn?: IntegrationView }>) {
  return () => {
    const { label, info, conn } = handle.props;
    if (!info) return null;
    const calls = conn?.tools.syncTools ?? [];
    return (
      <Disclosure summary={`About the ${label} API`} testId="api-info">
        <div mix={stack}>
          <p mix={faint}>
            {info.api}, {info.apiVersion}. {info.sdk}
          </p>
          <p mix={faint}>Rate limit: {info.rateLimit}.</p>
          {calls.length > 0 ? <p mix={[faint, mono]}>{calls.join(' · ')}</p> : null}
          <ul mix={linkList}>
            {(info.docs ?? []).map((d) => (
              <li key={d.url}>
                <a href={d.url} target="_blank" rel="noopener noreferrer" data-rmx-document>
                  {d.title}
                </a>
              </li>
            ))}
          </ul>
        </div>
      </Disclosure>
    );
  };
}

const cap = (s: string): string => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);
/** "Linear GraphQL API (https://...)" without the address in brackets. */
const apiName = (api: string | undefined): string => (api ?? '').replace(/\s*\([^)]*\)\s*$/, '').trim();

/** Not connected: the three steps, the connect form, what it reads. */
function SetupFlow(
  handle: Handle<{ kind: ServiceKind; info?: IntegrationKindInfo; hasAgentTools: boolean; onConnected: (test?: IntegrationTestReport) => void; onKindsChanged?: () => void }>,
) {
  return () => {
    const { kind, info, hasAgentTools, onConnected, onKindsChanged } = handle.props;
    const label = info?.label ?? kind.label;
    const items = info?.items ?? 'items';
    const api = apiName(info?.api);
    return (
      <>
        <DetailHeader
          title={<span data-testid="integration-name">{label}</span>}
          logo={kind.kind}
          status={{ text: 'Not connected', tone: 'muted' }}
          sub={info?.blurb ?? kind.blurb}
        />
        <ol mix={stepsCss} aria-label="Steps" data-testid="setup-steps">
          <li aria-current="step">
            <span>1</span>Connect
          </li>
          <li>
            <span>2</span>Check your account
          </li>
          <li>
            <span>3</span>Assign to a project
          </li>
        </ol>
        <ConnectForm kind={kind.kind} info={info} onDone={(r) => onConnected(r.test)} onKindsChanged={onKindsChanged} />
        <SettingsGroup title="What it reads" testId="integration-reads" bare>
          <ul mix={readsCss}>
            <li>
              <Icon icon={ShieldCheck} size={13} />
              Only the {items} the credential can see{api ? `, through the ${api}` : ''}
            </li>
            <li>
              <Icon icon={ShieldCheck} size={13} />
              Their text arrives as untrusted text you review before it becomes a task
            </li>
            <li>
              <Icon icon={ShieldCheck} size={13} />
              {hasAgentTools ? `${label}'s own MCP server for agents is under Agent tools` : 'Nothing is written back without your approval'}
            </li>
          </ul>
          <LearnMore topic="integrations" label="How Drydock keeps this safe" />
        </SettingsGroup>
      </>
    );
  };
}

/** Opens a page of the app (the page passes navigate; tests record the address). */
export type Open = (href: string) => void;

/** Connected: the header with Sync now and the … menu, then the connection, the checks and the projects as rows. */
function ConnectedService(
  handle: Handle<{
    kind: ServiceKind;
    info?: IntegrationKindInfo;
    conn: IntegrationView;
    /** The last test report (the one connecting made, or Test's), kept by the page across the setup and this view. */
    report?: IntegrationTestReport;
    onReport: (r: IntegrationTestReport | undefined) => void;
    onChanged: () => void;
    onKindsChanged?: () => void;
    open: Open;
  }>,
) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let checked: IntegrationVerifyReport | undefined;
  let synced = '';
  let reconnect = false;
  let confirm = false;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const openProject = (id: string) => {
    s.workspace.selectProject(id);
    handle.props.open(routes.projectIntegrations.href());
  };

  return () => {
    const { kind, info, conn, report, onReport, onChanged, onKindsChanged } = handle.props;
    const label = info?.label ?? kind.label;
    const id = conn.identity ?? {};
    const assignments = conn.assignments ?? [];
    const st = serviceState(conn);
    const projects = s.workspace.projectList;
    const nameOf = (pid: string) => projects.find((p) => p.id === pid)?.name ?? pid;
    const free = projects.filter((p) => !assignments.some((a) => a.projectId === p.id));
    // what this connection can write back (its own list), else what the kind can
    const ops = (conn.tools.ops ?? []).length > 0 ? (conn.tools.ops ?? []) : (info?.ops ?? []);
    const notReported = conn.checkedAt ? 'not reported by the API' : 'not checked yet';
    const workspaceHref = id.workspaceUrl ? safeHref(id.workspaceUrl) : undefined;
    return (
      <>
        <DetailHeader
          title={<span data-testid="integration-name">{label}</span>}
          logo={kind.kind}
          status={st.bucket === 'needs' ? { text: 'Needs you', tone: 'warn' } : { text: 'Connected', tone: 'ok' }}
          sub={<span data-testid="integration-line">{syncLine(conn)}</span>}
          actions={
            <>
              {assignments.length > 0 ? (
                <Button
                  size="sm"
                  icon={RefreshCw}
                  disabled={act.busy !== ''}
                  mix={[
                    on('click', async () => {
                      const r = await act.run('sync', () => s.client.integrationSync(conn.id));
                      if (r) set(() => (synced = syncText(r)));
                      onChanged();
                    }),
                  ]}
                >
                  {act.busy === 'sync' ? 'Syncing...' : 'Sync now'}
                </Button>
              ) : null}
              <Menu
                label={`More for ${label}`}
                placement="bottom-end"
                items={[
                  { name: 'reconnect', label: 'Reconnect', icon: KeyRound },
                  { name: 'disconnect', label: 'Disconnect', icon: Trash2, danger: true, separatorBefore: true },
                ]}
                onSelect={(name) => set(() => (name === 'reconnect' ? (reconnect = true) : (confirm = true)))}
                trigger={(mix) => (
                  <button type="button" mix={[moreCss, mix]} aria-label={`More for ${label}`} data-testid="integration-more">
                    <Icon icon={Ellipsis} size={15} />
                  </button>
                )}
              />
            </>
          }
        />
        <ErrorNote error={act.error} />
        {synced ? (
          <p mix={muted} role="status" data-testid="integration-synced">
            {synced}
          </p>
        ) : null}
        {confirm ? (
          <div mix={confirmCss} data-testid="disconnect-confirm">
            <span>Disconnecting removes the credential, every project assignment and the items that never became tasks.</span>
            <Button
              size="sm"
              variant="danger"
              disabled={act.busy !== ''}
              mix={[
                on('click', async () => {
                  await act.run('disconnect', () => s.client.integrationDisconnect(conn.id));
                  onReport(undefined);
                  checked = undefined;
                  confirm = false;
                  onChanged();
                }),
              ]}
            >
              Yes, disconnect {label}
            </Button>
            <Button size="sm" mix={[on('click', () => set(() => (confirm = false)))]}>
              Keep
            </Button>
          </div>
        ) : null}
        <SettingsGroup title="Connection" testId="connection-identity">
          {conn.statusDetail ? <SettingRow label="State">{conn.statusDetail}</SettingRow> : null}
          {/* what the API reported, never typed: the workspace and who the credential is, one line */}
          <SettingRow label="Workspace">
            {workspaceHref ? (
              <a href={workspaceHref} target="_blank" rel="noopener noreferrer" data-rmx-document>
                {id.workspace || id.workspaceUrl}
              </a>
            ) : (
              id.workspace || notReported
            )}
            <span mix={muted}> · {id.account ? `read as ${id.account}` : `account ${notReported}`}</span>
          </SettingRow>
          <SettingRow label="Signed in with">
            {id.method || conn.authKind}
            <span mix={muted}>
              {' · '}
              {conn.kind === 'github' && conn.authKind === 'gh' ? 'your gh sign-in (this connection stores no GitHub token)' : conn.hasCredential ? 'stored in the credential store (never shown)' : 'no credential'}
            </span>
          </SettingRow>
          {(id.scopes ?? []).length > 0 ? <SettingRow label="Scopes">{(id.scopes ?? []).join(', ')}</SettingRow> : null}
          <SettingRow label="Imports">{cap(info?.items ?? 'items')}, as untrusted text you review</SettingRow>
          {ops.length > 0 ? <SettingRow label="Writes back">{ops.map((o) => o.label).join(', ')}: each after you approve it</SettingRow> : null}
        </SettingsGroup>
        {reconnect ? (
          <div mix={stack}>
            <ConnectForm kind={kind.kind} info={info} reconnectId={conn.id} onDone={(r) => (onReport(r.test), (checked = undefined), (reconnect = false), onChanged())} onKindsChanged={onKindsChanged} />
            <div>
              <Button size="sm" variant="ghost" mix={[on('click', () => set(() => (reconnect = false)))]}>
                Cancel the reconnect
              </Button>
            </div>
          </div>
        ) : null}
        <SettingsGroup title="Checks" testId="integration-checks">
          <SettingRow
            label="Connection"
            control={
              <Button
                size="sm"
                icon={Stethoscope}
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    const r = await act.run('test', () => s.client.integrationTest(conn.id));
                    if (r) onReport(r);
                    onChanged();
                  }),
                ]}
              >
                Test
              </Button>
            }
          >
            {conn.checkedAt ? `Last tested ${formatDateTime(conn.checkedAt)}` : 'Never tested'}
          </SettingRow>
          <SettingRow
            label="Your account"
            control={
              <Button
                size="sm"
                icon={ShieldCheck}
                disabled={act.busy !== ''}
                title={`Reads your ${label} account with the credential Drydock already holds: who it is, one list, one page of ${info?.items ?? 'items'}. Nothing is written and nothing read is kept.`}
                mix={[
                  on('click', async () => {
                    const r = await act.run('verify', () => s.client.integrationVerify(conn.id));
                    if (r) checked = r;
                    onChanged();
                  }),
                ]}
              >
                {act.busy === 'verify' ? 'Checking...' : 'Check against your account'}
              </Button>
            }
          >
            <span data-testid="integration-verified" data-ok={conn.verified ? String(conn.verified.ok) : undefined}>
              {verifiedText(conn)}
            </span>
          </SettingRow>
        </SettingsGroup>
        {report ? <ReportView r={report} /> : null}
        {checked ? <VerifyReportView r={checked} /> : null}
        <SettingsGroup
          title="Projects"
          testId="integration-projects"
          actions={
            free.length > 0 ? (
              <Menu
                label="Assign to a project"
                placement="bottom-end"
                items={free.map((p) => ({ name: p.id, label: p.name, icon: FolderPlus }))}
                onSelect={openProject}
                trigger={(mix) => (
                  <button type="button" mix={[...buttonStyle('ghost', 'sm'), mix]} data-testid="assign-to-project">
                    <Icon icon={FolderPlus} size={14} />
                    Assign to a project
                  </button>
                )}
              />
            ) : projects.length === 0 ? (
              <a href={newProjectHref()} mix={buttonStyle('ghost', 'sm')}>
                Create a project
              </a>
            ) : undefined
          }
        >
          {(conn.legacyItems ?? 0) > 0 ? (
            <SettingRow label="Stored items">
              {plural(conn.legacyItems ?? 0, 'item')} {conn.legacyItems === 1 ? 'was' : 'were'} stored before connections and projects were separated; the first project you assign adopts them and the stored settings.
            </SettingRow>
          ) : null}
          {assignments.map((a) => (
            <SettingRow
              key={a.id}
              label={nameOf(a.projectId)}
              testId="integration-project"
              control={
                <Button size="sm" variant="ghost" icon={FolderOpen} aria-label={`Open ${nameOf(a.projectId)}`} mix={[on('click', () => openProject(a.projectId))]}>
                  Open
                </Button>
              }
            >
              <span mix={muted}>
                {scopeSummary(info, a)} · {a.sync?.state === 'ok' ? `${plural(a.sync.itemsSeen, info?.items === 'pages' ? 'page' : 'item')}` : `sync: ${a.sync?.state ?? 'never'}`}
              </span>
            </SettingRow>
          ))}
          {assignments.length === 0 ? (
            <SettingRow label="None yet">
              <span mix={muted}>Assign it to a project to import its {info?.items ?? 'items'} there.</span>
            </SettingRow>
          ) : null}
        </SettingsGroup>
        <p mix={faint} data-testid="integration-assigned">
          {assignments.length === 0 ? 'Not assigned to a project yet.' : `Assigned to ${plural(assignments.length, 'project')}.`} What each project imports is chosen on its own Integrations
          page.
        </p>
        <ApiFacts label={label} info={info} conn={conn} />
      </>
    );
  };
}

/**
 * One service, as the list's detail: the setup while it is not connected, the connection's rows once it is. The vendor's MCP
 * servers and skills are its Agent tools row (vendor-detail.tsx).
 */
export function ServiceView(
  handle: Handle<{
    kind: ServiceKind;
    info?: IntegrationKindInfo;
    conn?: IntegrationView;
    hasAgentTools: boolean;
    onChanged: () => void;
    onKindsChanged?: () => void;
    open: Open;
  }>,
) {
  // the test report outlives the switch from the setup to the connection's rows: connecting tests the credential, and says so there
  let report: IntegrationTestReport | undefined;
  const setReport = (r: IntegrationTestReport | undefined) => {
    report = r;
    void handle.update();
  };
  return () => {
    const { kind, info, conn, hasAgentTools, onChanged, onKindsChanged, open } = handle.props;
    const label = info?.label ?? kind.label;
    return (
      <article mix={detailCss} data-testid="integration" data-kind={kind.kind} data-name={label} data-connected={String(!!conn)}>
        {conn ? (
          <ConnectedService key={conn.id} kind={kind} info={info} conn={conn} report={report} onReport={setReport} onChanged={onChanged} onKindsChanged={onKindsChanged} open={open} />
        ) : (
          <>
            <SetupFlow kind={kind} info={info} hasAgentTools={hasAgentTools} onConnected={(r) => (setReport(r), onChanged())} onKindsChanged={onKindsChanged} />
            {report ? <ReportView r={report} /> : null}
            <ApiFacts label={label} info={info} />
          </>
        )}
      </article>
    );
  };
}

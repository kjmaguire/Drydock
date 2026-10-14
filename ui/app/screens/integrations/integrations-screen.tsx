// Integrations (decisions P42, P43 and P47): one workspace page for everything Drydock connects to outside itself, a tile per
// vendor. A vendor's tile opens its panel with up to three sections. App sync is Drydock's own connection to Linear, Notion,
// Sentry, Aikido or GitHub (P42): connected once for the whole workspace; the name and logo are fixed, what a connection reaches
// (account, workspace, scopes) is read from the service's API, never typed. Agent tools (MCP) are the vendor's connector cards
// (P43, screens/mcp/gallery.tsx): connected once. Skills from this vendor names the vendor's skill registries and links to the
// skill library. Below the tiles: your own MCP servers. Nothing of a single project is on this page (P51): assigning a
// connection to a project (what it imports, its items, rules and outbound writes), adding a server to a project, granting it to
// the project's agents and the calls waiting for approval are on the project's Integrations page
// (project-integrations-screen.tsx, /project/integrations). /mcp is an alias of this page.

import { css, on, ref, type Handle } from 'remix/component';
import { Blocks, Check, Cpu, ExternalLink, KeyRound, LogIn, Server, ShieldAlert, Stethoscope, Trash2, Wrench, X } from 'lucide';
import type { IntegrationAuthMethod, IntegrationKindInfo, IntegrationOAuthSession, IntegrationTestReport, IntegrationView, SkillRegistry } from '../../api/extTypes.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { BrandLogo } from '../../ui/brand-logo.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { card, Page, Section, stack } from '../../ui/layout.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { ConnectorCard, connectorGrid, OwnServers } from '../mcp/gallery.tsx';
import { full, head, panel, push, toolbar } from './assignments.tsx';
import { KINDS, kindLabel, statusTone, syncTone, tileState, vendors, vendorStateText, vendorTone, type ServiceKind, type Vendor } from './integrations.ts';

const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd, maxWidth: '86ch' });
const cardCss = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
const stats = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))',
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
const steps = css({ display: 'grid', gap: '4px', margin: '0', padding: '0', listStyle: 'none', fontSize: t.textMd, '& li': { display: 'flex', gap: t.s2, alignItems: 'baseline' } });
const reportCss = css({ display: 'grid', gap: t.s2, padding: t.s3, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp, '& p': { margin: '0' } });
const detailsCss = css({ gridColumn: '1 / -1', '& summary': { cursor: 'pointer', color: t.textMuted, fontSize: t.textSm, marginBottom: t.s2 } });
const serviceHead = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flexWrap: 'wrap',
  '& h2': { fontSize: t.textXl, fontWeight: '600', margin: '0' },
});
const linkList = css({ display: 'flex', gap: t.s3, flexWrap: 'wrap', margin: '0', padding: '0', listStyle: 'none', fontSize: t.textSm });
const methodGrid = css({ gridColumn: '1 / -1', display: 'flex', gap: t.s2, flexWrap: 'wrap', margin: '0', padding: '0', border: '0', '& legend': { fontSize: t.textSm, fontWeight: '500', marginBottom: '6px', padding: '0' } });
const methodTile = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: `6px ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  cursor: 'pointer',
  fontSize: t.textSm,
  '&:has(input:checked)': { borderColor: t.accent, background: t.accentSoft },
  '&:has(input:disabled)': { opacity: '0.6', cursor: 'not-allowed' },
});
const identityGrid = css({ gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' });
const codeCss = css({ fontFamily: t.mono, fontSize: t.textLg, fontWeight: '600', letterSpacing: '0.08em' });
const panelStack = css({ display: 'grid', gap: t.s5, minWidth: '0' });
const regList = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gap: t.s2, padding: t.s3, border: `1px solid ${t.border}`, borderRadius: t.rMd, background: t.bgSurface },
  '& p': { margin: '0' },
});

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

/** Connect (or reconnect) one service. The secret fields are uncontrolled: read once, cleared before the request leaves. */
function ConnectForm(handle: Handle<{ kind: string; info?: IntegrationKindInfo; reconnectId?: string; onDone: (r: { test?: IntegrationTestReport }) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
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
  const finish = async (req: Record<string, unknown>) => {
    const r = await act.run('connect', () => s.client.integrationConnect({ kind: handle.props.kind, id: handle.props.reconnectId, ...req } as never));
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
    const { kind, info, reconnectId } = handle.props;
    const ro = s.workspace.license;
    const label = info?.label ?? kindLabel(kind);
    const methods: IntegrationAuthMethod[] = info?.auth ?? [];
    const cur = methods.find((m) => m.id === method) ?? methods.find((m) => m.available) ?? methods[0];
    const fields = cur?.fields ?? [];
    const plain = fields.filter((f) => !f.advanced);
    const advanced = fields.filter((f) => f.advanced);
    const signHref = oauth?.url ? safeHref(oauth.url) : undefined;
    const submit = async (e: SubmitEvent) => {
      e.preventDefault();
      if (!cur || cur.id === 'oauth') return;
      const req: Record<string, unknown> = {};
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
      await finish(req);
    };
    const renderField = (f: NonNullable<IntegrationAuthMethod['fields']>[number]) => (
      <Field key={`${kind}-${cur?.id}-${f.key}`} label={f.label} hint={f.hint}>
        {(id) =>
          f.secret ? (
            <input
              id={id}
              type="password"
              autocomplete="new-password"
              spellcheck={false}
              name={`integration-${f.key}`}
              aria-label={f.label}
              mix={ref((n: HTMLInputElement) => (secrets[f.key] = n))}
            />
          ) : f.options && f.options.length > 0 ? (
            <select id={id} aria-label={f.label} value={values[f.key] ?? f.options[0]!.value} mix={on('change', (e) => upd(() => (values = { ...values, [f.key]: e.currentTarget.value })))}>
              {f.options.map((o) => (
                <option key={o.value} value={o.value} selected={o.value === (values[f.key] ?? f.options![0]!.value)}>
                  {o.label}
                </option>
              ))}
            </select>
          ) : (
            <input id={id} value={values[f.key] ?? ''} spellcheck={false} mix={on('input', (e) => upd(() => (values = { ...values, [f.key]: e.currentTarget.value })))} />
          )
        }
      </Field>
    );
    return (
      <form mix={[card, formGrid, on<HTMLFormElement, 'submit'>('submit', submit)]} autocomplete="off" aria-label={reconnectId ? `Reconnect ${label}` : `Connect ${label}`}>
        <div mix={full}>
          <ErrorNote error={act.error} what="Not connected" />
        </div>
        {methods.length > 1 ? (
          <fieldset mix={methodGrid}>
            <legend>How to connect</legend>
            {methods.map((m) => (
              <label key={m.id} mix={methodTile} title={m.note}>
                <input type="radio" name={`int-method-${kind}`} value={m.id} checked={cur?.id === m.id} disabled={!m.available} mix={on('change', () => upd(() => (method = m.id)))} />
                {m.label}
              </label>
            ))}
          </fieldset>
        ) : null}
        {cur?.note ? <p mix={[full, faint]}>{cur.note}</p> : null}
        {kind === 'github' ? (
          <div mix={full} data-testid="gh-status">
            {gh.loading && !gh.data ? <Loading label="Checking gh" /> : null}
            {gh.data?.loggedIn ? (
              <Notice tone="ok">gh is signed in as {gh.data.login}. Drydock uses that sign-in and stores no GitHub token.</Notice>
            ) : gh.data ? (
              <Notice tone="warn">{gh.data.error || 'gh is not signed in'}. The Connections screen shows the GitHub CLI.</Notice>
            ) : null}
          </div>
        ) : null}
        {cur?.id === 'oauth' ? (
          <div mix={[full, stack]}>
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
        {advanced.length > 0 ? (
          <details mix={detailsCss}>
            <summary>Advanced</summary>
            <div mix={formGrid}>{advanced.map(renderField)}</div>
          </details>
        ) : null}
        <div mix={full}>
          <ReadOnlyNote />
        </div>
        {cur?.id !== 'oauth' ? (
          <div mix={formActions}>
            <Button variant="primary" type="submit" icon={kind === 'github' ? Blocks : KeyRound} disabled={act.busy !== '' || ro.readOnly || !cur} title={ro.readOnly ? ro.reason : undefined}>
              {reconnectId ? `Reconnect ${label}` : kind === 'github' ? 'Use the GitHub CLI sign-in' : `Connect ${label}`}
            </Button>
          </div>
        ) : null}
      </form>
    );
  };
}


/**
 * One service, opened from its tile: its fixed name and logo and what the connection is, with Test, Reconnect and Disconnect.
 * How many projects it is assigned to is a count: assigning is done on a project's own Integrations page.
 */
function ServicePanel(handle: Handle<{ kind: ServiceKind; info?: IntegrationKindInfo; conn?: IntegrationView; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let report: IntegrationTestReport | undefined;
  let reconnect = false;
  let confirm = false;
  return () => {
    const { kind, info, conn, onChanged } = handle.props;
    const label = info?.label ?? kind.label;
    const id = conn?.identity ?? {};
    const assigned = (conn?.assignments ?? []).length;
    return (
      <article mix={[card, cardCss]} data-testid="integration" data-kind={kind.kind} data-name={label} aria-labelledby={`svc-${handle.id}`}>
        <header mix={serviceHead}>
          <BrandLogo id={kind.kind} size={40} label />
          <h2 id={`svc-${handle.id}`} data-testid="integration-name">
            {label}
          </h2>
          {conn ? <Badge tone={statusTone(conn.status)}>{conn.status}</Badge> : <Badge tone="muted">not connected</Badge>}
          {conn ? <Badge tone={syncTone(conn.sync.state)}>sync: {conn.sync.state}</Badge> : null}
        </header>
        <p mix={muted}>
          {info?.blurb ?? kind.blurb}. Drydock reads {info?.items ?? 'items'} through {info?.api ?? 'the service API'} and imports them into the projects you assign.
        </p>
        {!conn ? (
          <ConnectForm kind={kind.kind} info={info} onDone={(r) => ((report = r.test), onChanged())} />
        ) : (
          <>
            {conn.statusDetail ? <p mix={muted}>{conn.statusDetail}</p> : null}
            <dl mix={[stats, identityGrid]} data-testid="connection-identity">
              <div>
                <dt>Account</dt>
                <dd>{id.account || (conn.checkedAt ? 'not reported by the API' : 'not checked yet')}</dd>
              </div>
              <div>
                <dt>Workspace</dt>
                <dd>
                  {id.workspaceUrl && safeHref(id.workspaceUrl) ? (
                    <a href={safeHref(id.workspaceUrl)} target="_blank" rel="noopener noreferrer" data-rmx-document>
                      {id.workspace || id.workspaceUrl}
                    </a>
                  ) : (
                    id.workspace || (conn.checkedAt ? 'not reported by the API' : 'not checked yet')
                  )}
                </dd>
              </div>
              <div>
                <dt>Connected with</dt>
                <dd>{id.method || conn.authKind}</dd>
              </div>
              <div>
                <dt>Scopes</dt>
                <dd>{(id.scopes ?? []).length > 0 ? (id.scopes ?? []).join(', ') : 'not reported by the API'}</dd>
              </div>
              <div>
                <dt>Last checked</dt>
                <dd>{conn.checkedAt ? formatDateTime(conn.checkedAt) : 'never'}</dd>
              </div>
              <div>
                <dt>Credential</dt>
                <dd>{conn.kind === 'github' ? 'your gh sign-in (Drydock stores no GitHub token)' : conn.hasCredential ? 'stored in the credential store (never shown)' : 'none'}</dd>
              </div>
            </dl>
            <ErrorNote error={act.error} />
            {report ? <ReportView r={report} /> : null}
            <div mix={toolbar}>
              <Button
                size="sm"
                icon={Stethoscope}
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    const r = await act.run('test', () => s.client.integrationTest(conn.id));
                    if (r) report = r;
                    onChanged();
                  }),
                ]}
              >
                Test
              </Button>
              <Button size="sm" icon={KeyRound} aria-expanded={reconnect ? 'true' : 'false'} mix={[on('click', () => ((reconnect = !reconnect), void handle.update()))]}>
                Reconnect
              </Button>
              <span mix={push}>
                {confirm ? (
                  <>
                    <Button
                      size="sm"
                      variant="danger"
                      mix={[
                        on('click', async () => {
                          await act.run('disconnect', () => s.client.integrationDisconnect(conn.id));
                          report = undefined;
                          confirm = false;
                          onChanged();
                        }),
                      ]}
                    >
                      Yes, disconnect {label}
                    </Button>
                    <Button size="sm" mix={[on('click', () => ((confirm = false), void handle.update()))]}>
                      Keep
                    </Button>
                  </>
                ) : (
                  <Button size="sm" variant="ghost" icon={Trash2} mix={[on('click', () => ((confirm = true), void handle.update()))]}>
                    Disconnect
                  </Button>
                )}
              </span>
            </div>
            {confirm ? <p mix={faint}>Disconnecting removes the credential, every project assignment and the items that never became tasks.</p> : null}
            {reconnect ? <ConnectForm kind={kind.kind} info={info} reconnectId={conn.id} onDone={(r) => ((report = r.test), (reconnect = false), onChanged())} /> : null}
            <section aria-label="Projects" mix={panel}>
              <h4>Projects</h4>
              {(conn.legacyItems ?? 0) > 0 ? (
                <Notice tone="info">
                  {conn.legacyItems} item(s) were stored before connections and projects were separated; the first project you assign adopts them and the stored settings.
                </Notice>
              ) : null}
              <p mix={muted} data-testid="integration-assigned">
                {assigned === 0 ? 'Not assigned to a project yet.' : `Assigned to ${assigned} project${assigned === 1 ? '' : 's'}.`} Assign it to a project, and choose what that project
                imports, on the project's Integrations page.
              </p>
            </section>
          </>
        )}
        {info ? (
          <details mix={detailsCss} data-testid="api-info">
            <summary>About the {label} API</summary>
            <div mix={stack}>
              <p mix={faint}>
                {info.api}, {info.apiVersion}. {info.sdk}
              </p>
              <p mix={faint}>Rate limit: {info.rateLimit}.</p>
              {(conn?.tools.syncTools ?? []).length > 0 ? <p mix={[faint, mono]}>{(conn?.tools.syncTools ?? []).join(' · ')}</p> : null}
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
          </details>
        ) : null}
        {report && !conn ? <ReportView r={report} /> : null}
      </article>
    );
  };
}

/** One vendor on the overview: its fixed logo and name, what it offers, and the state of each channel it has. */
function VendorTile(handle: Handle<{ v: Vendor; conn?: IntegrationView; selected: boolean; onSelect: () => void }>) {
  return () => {
    const { v, conn, selected, onSelect } = handle.props;
    const data: Record<string, string> = { kind: v.key };
    if (v.kind) data.connected = conn ? 'true' : 'false';
    else data.mcp = 'true';
    return <Tile title={v.label} logo={v.logo} icon={Server} blurb={v.blurb} state={vendorStateText(v, conn)} tone={vendorTone(v, conn)} selected={selected} onSelect={onSelect} testId="integration-tile" data={data} />;
  };
}

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
      <Section
        label="Skills from this vendor"
        testId="integration-skills"
        title="Skills from this vendor"
        description={`Skill registries ${v.label} publishes. Their skills are in the skill library, where you add them to a project.`}
      >
        <ErrorNote error={error} what="Skills" />
        {loading && !registries ? <Loading label="Loading the registries..." /> : null}
        <ul mix={regList} aria-label={`Skill registries of ${v.label}`}>
          {v.skillRegistries.map((id) => {
            const r = (registries ?? []).find((x) => x.id === id);
            const selective = r?.importMode === 'selective_only';
            return (
              <li key={id} data-testid="vendor-registry" data-registry={id}>
                <div mix={head}>
                  <Icon icon={Cpu} size={14} />
                  <strong>{r?.repo ?? id}</strong>
                  {r ? (
                    <span mix={faint}>
                      {r.license ? `license ${r.license}` : ''}
                      {r.enabled ? '' : ' · disabled'}
                    </span>
                  ) : registries ? (
                    <span mix={faint}>not in this workspace's registry list</span>
                  ) : null}
                </div>
                {r && selective ? (
                  <span mix={muted} data-testid="vendor-selective">
                    {r.notes ? `${r.notes}: ` : ''}pick its skills one at a time in the {libraryLink}.
                  </span>
                ) : null}
              </li>
            );
          })}
        </ul>
        <p mix={faint}>Every registry's skills, and adding them to a project, are in the {libraryLink} (Library &gt; Skills).</p>
      </Section>
    );
  };
}

/** The panel of the selected vendor: app sync (an integration kind), agent tools (its connector cards) and its skill registries. */
function VendorPanel(
  handle: Handle<{
    v: Vendor;
    info?: IntegrationKindInfo;
    conn?: IntegrationView;
    registries: SkillRegistry[] | undefined;
    registriesLoading: boolean;
    registriesError?: Error;
    onSyncChanged: () => void;
    onAgentsChanged: () => void;
  }>,
) {
  return () => {
    const { v, info, conn, registries, registriesLoading, registriesError, onSyncChanged, onAgentsChanged } = handle.props;
    return (
      <div mix={panelStack} data-testid="integration-panel" data-kind={v.key}>
        {v.kind ? (
          <Section
            label="App sync"
            testId="integration-sync"
            title="App sync"
            description={`Drydock itself reads ${info?.items ?? 'items'} from ${v.label}, as untrusted text, and imports them into each project it is assigned to on that project's Integrations page.`}
          >
            <ServicePanel kind={v.kind} info={info} conn={conn} onChanged={onSyncChanged} />
          </Section>
        ) : null}
        {v.cards.length > 0 ? (
          <Section
            label="Agent tools (MCP)"
            testId="integration-agents"
            title="Agent tools (MCP)"
            description={`${v.label}'s MCP server${v.cards.length === 1 ? '' : 's'}, checked against the vendor's own documentation. Connect once here; add it to a project, and grant it to that project's agents, from the project's Integrations page. Every tool not marked read-only waits for your approval, call by call.`}
          >
            <ul mix={connectorGrid}>
              {v.cards.map((c) => (
                <ConnectorCard key={c.id} card={c} onChanged={onAgentsChanged} />
              ))}
            </ul>
          </Section>
        ) : null}
        {v.skillRegistries.length > 0 ? <VendorSkills v={v} registries={registries} loading={registriesLoading} error={registriesError} /> : null}
      </div>
    );
  };
}

/** The workspace's Integrations page: connect each service and MCP server once. `initial` opens that vendor's tile. */
export function IntegrationsScreen(handle: Handle<{ initial?: string }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.integrationList(), when: (e) => e.type.startsWith('integration.') });
  const kinds = query(handle, { load: () => s.client.integrationKinds() });
  const gallery = query(handle, { load: () => s.client.mcpGallery(), when: (e) => e.type.startsWith('mcp.server') });
  // the registries are read once, and only when a vendor on the page publishes some
  const regs = query(handle, { load: () => s.client.skillRegistries(), enabled: () => (gallery.data ?? []).some((c) => (c.skillRegistries ?? []).length > 0) });
  let selected = handle.props.initial ?? '';
  return () => {
    const list: IntegrationView[] = q.data ?? [];
    const cards: McpGalleryCard[] = gallery.data ?? [];
    const all = vendors(KINDS, cards);
    const current = all.find((v) => v.key === selected);
    const info = (kinds.data ?? []).find((k) => k.kind === selected);
    const connected = list.length + cards.filter((c) => c.status === 'connected').length;
    const loaded = !!q.data && !!gallery.data;
    return (
      <Page
        title="Integrations"
        icon={Blocks}
        subtitle={loaded ? `${connected} connected` : undefined}
        actions={
          <a href={routes.library.href()} mix={buttonStyle('ghost', 'sm')}>
            <Icon icon={Wrench} size={14} />
            Connect servers in the Library
          </a>
        }
      >
        <p mix={introCss}>
          Connect a service once for the whole workspace. App sync imports its items into the projects it is assigned to: everything a service returns is outside text, redacted,
          scored for injection risk and quoted as data, and writing back needs your approval of the exact text. Agent tools reach agents through MCP: a server is connected once,
          then added to a project and granted per agent, a server nobody was granted is never reached, and every tool you have not marked read-only waits for your approval, call
          by call. Secrets stay in the Windows credential store. Assigning a service to a project, adding a server to it and granting it to its agents are on that project's
          Integrations page. Claude Code agents receive MCP servers; another agent CLI receives them only after you allow it under MCP gateway on the{' '}
          <a href={routes.connections.href()}>Connections</a> page.
        </p>
        <div data-testid="connector-gallery" mix={panelStack}>
          <ErrorNote error={q.error ?? kinds.error ?? gallery.error} what="Integrations" />
          {(q.loading && !q.data) || (gallery.loading && !gallery.data) ? <Loading /> : null}
          {loaded && connected === 0 ? (
            <p mix={emptyLine}>
              <Icon icon={ShieldAlert} size={14} />
              <strong>Nothing is connected.</strong> Choose a service below. Its items arrive as untrusted text you can read, score and import; its tools reach only the agents you grant.
            </p>
          ) : null}
          <ul mix={tileGrid} aria-label="Services">
            {all.map((v) => (
              <VendorTile
                key={v.key}
                v={v}
                conn={v.kind ? tileState(list, v.key) : undefined}
                selected={selected === v.key}
                onSelect={() => ((selected = selected === v.key ? '' : v.key), void handle.update())}
              />
            ))}
          </ul>
          {current ? (
            <VendorPanel
              key={current.key}
              v={current}
              info={info}
              conn={current.kind ? tileState(list, current.key) : undefined}
              registries={regs.data}
              registriesLoading={regs.loading}
              registriesError={regs.error}
              onSyncChanged={q.reload}
              onAgentsChanged={gallery.reload}
            />
          ) : null}
        </div>
        <OwnServers linked={cards.map((c) => c.libraryId)} onChanged={gallery.reload} />
      </Page>
    );
  };
}


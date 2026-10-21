// License: the state of the installed license (or that licensing is not configured in this build), what read-only means,
// importing a license (always allowed, also while read-only) and removing it behind a confirmation. Workspace screen: it
// works with no project, so a read-only install can always reach it.

import { css, on, ref, type Handle } from 'remix/component';
import { CircleCheck, CircleSlash, FileKey, FlaskConical, KeyRound, Lock, RotateCcw, ShieldAlert, Trash2, TriangleAlert } from 'lucide';
import type { LicenseView } from '../../api/pkgTypes.ts';
import { starts } from '../../state/live.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, kv, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { Callout, confirmRow, DangerZone, Done, Group, intro, panel, Row, settingsColumn } from '../settings/settings-kit.tsx';
import { clockNote, daysLabel, daysText, messageCarriesClockNote, READONLY_OFF, READONLY_USABLE, stateText, stateTone } from './license.ts';

const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  '& .ico': { display: 'grid', placeItems: 'center', width: '36px', height: '36px', borderRadius: t.rMd, flex: 'none' },
  '& .msg': { display: 'grid', gap: '2px', minWidth: '0' },
  '& .msg p': { margin: '0', color: t.textMuted, fontSize: t.textSm },
  '& .badges': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
});

const cols = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))',
  gap: t.s4,
  '& h3': { fontSize: t.textSm, fontWeight: '600', marginBottom: t.s1 },
  '& ul': { margin: '0', paddingLeft: '18px', display: 'grid', gap: '2px' },
});

const clockCss = css({ display: 'grid', gap: t.s2, margin: '0', padding: '8px 12px', borderRadius: t.rMd, background: t.dangerSoft, fontSize: t.textMd, '& p': { margin: '0' } });

const formCss = css({ display: 'grid', gap: t.s4 });

/** Error notes on this page also carry the error-note class (the end-to-end suite reads the core's refusal through it). */
function PageError(handle: Handle<{ error: unknown }>) {
  return () =>
    handle.props.error ? (
      <div class="error-note">
        <ErrorNote error={handle.props.error} what="License" />
      </div>
    ) : null;
}

function Unconfigured(handle: Handle<{ view: LicenseView }>) {
  return () => (
    <Callout tone="info" icon={FlaskConical} role="status" testId="license-unconfigured" title="Licensing is not configured in this build">
      <p>
        <strong>This is a development or unlicensed build. Every feature is allowed and nothing is restricted.</strong> There is no license to import here: a release
        build that carries license verification keys shows your license on this page instead.
      </p>
      <p mix={muted}>{handle.props.view.message}</p>
    </Callout>
  );
}

function Details(handle: Handle<{ v: LicenseView }>) {
  return () => {
    const v = handle.props.v;
    const tn = stateTone(v.state);
    return (
      <dl mix={kv} data-testid="license-details">
        <dt>State</dt>
        <dd>
          <Badge tone={tn}>
            <span data-testid="license-state">{stateText(v.state)}</span>
          </Badge>{' '}
          {v.readOnly ? <Badge tone="err">read-only</Badge> : null}
        </dd>
        {v.state !== 'none' ? (
          <>
            <dt>{daysLabel(v)}</dt>
            <dd data-testid="license-days">{daysText(v)}</dd>
          </>
        ) : null}
        {v.kind ? (
          <>
            <dt>Kind</dt>
            <dd>{v.kind}</dd>
          </>
        ) : null}
        {v.plan ? (
          <>
            <dt>Plan</dt>
            <dd>{v.plan}</dd>
          </>
        ) : null}
        {v.name ? (
          <>
            <dt>Licensed to</dt>
            <dd>{v.name}</dd>
          </>
        ) : null}
        {v.subject ? (
          <>
            <dt>Subject</dt>
            <dd>{v.subject}</dd>
          </>
        ) : null}
        {v.licenseId ? (
          <>
            <dt>License id</dt>
            <dd>
              <code>{v.licenseId}</code>
            </dd>
          </>
        ) : null}
        <dt>Update channels</dt>
        <dd>{(v.channels ?? []).join(', ') || 'stable'}</dd>
        {(v.features ?? []).length > 0 ? (
          <>
            <dt>Features</dt>
            <dd>{(v.features ?? []).join(', ')}</dd>
          </>
        ) : null}
      </dl>
    );
  };
}

export function LicenseScreen(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.licenseStatus(), when: (e) => starts(e, 'license.') });
  const act = action(handle);
  // The token lives only in the uncontrolled input and is never shown back. It is cleared once the core accepted it; a failed
  // import leaves it in place so the person can fix a typo without pasting it again.
  let tokenEl: HTMLInputElement | undefined;
  let path = '';
  let confirmClear = false;
  let confirmReset = false;
  let done = '';
  let clockDone = '';

  const reloadAll = () => {
    q.reload();
    s.workspace.licenseLive.reload();
  };

  const submit = async () => {
    const token = tokenEl?.value.trim() ?? '';
    if (!token && !path.trim()) return;
    done = '';
    const r = await act.run('import', () => s.client.licenseImport(token ? { token } : { path: path.trim() }));
    if (r) {
      if (tokenEl) tokenEl.value = '';
      path = '';
      done = r.message;
      reloadAll();
    }
    void handle.update();
  };

  /** Puts the record of the latest date seen right; the core refuses unless it is ahead of this computer's clock, and audits both times. */
  const resetClock = async () => {
    confirmReset = false;
    clockDone = '';
    const r = await act.run('reset', () => s.client.licenseResetClock());
    if (r) {
      clockDone = "The clock check was reset: Drydock now judges the license by this computer's clock.";
      reloadAll();
    }
    void handle.update();
  };

  const clear = async () => {
    confirmClear = false;
    done = '';
    const r = await act.run('clear', () => s.client.licenseClear());
    if (r) {
      done = r.message;
      reloadAll();
    }
    void handle.update();
  };

  return () => {
    const v = q.data;
    const busy = act.busy !== '';
    const tn = v ? stateTone(v.state) : 'muted';
    return (
      <Page title="License" icon={KeyRound} subtitle={v ? (v.configured ? stateText(v.state) : 'Not configured in this build') : undefined}>
        <div mix={settingsColumn}>
          <PageError error={q.error ?? act.error} />
          {!v && q.loading ? <Loading /> : null}
          {v && !v.configured ? <Unconfigured view={v} /> : null}

          {v && v.configured ? (
            <Group
              title="Your license"
              icon={KeyRound}
              description="What the installed license allows, as your local core reads it."
            >
              <div mix={panel}>
                <div mix={headCss}>
                  <span class="ico" style={{ background: tone[tn].bg, color: tone[tn].fg }}>
                    <Icon icon={tn === 'ok' ? CircleCheck : tn === 'warn' ? ShieldAlert : CircleSlash} size={18} />
                  </span>
                  <div class="msg">
                    <strong>{stateText(v.state)}</strong>
                    <p data-testid="license-message">{v.message}</p>
                  </div>
                </div>
                <Details v={v} />
              </div>
              {v.clockSuspect ? (
                <div role="alert" mix={clockCss} data-testid="license-clock">
                  {messageCarriesClockNote(v) ? null : <p data-testid="license-clock-note">{clockNote(v)}</p>}
                  {confirmReset ? (
                    <div mix={confirmRow} role="group" aria-label="Confirm the clock check reset">
                      <span>
                        Reset the clock check? Drydock then takes this computer's clock as the latest date it has seen, and the license is judged by it. The reset is written to
                        the audit log.
                      </span>
                      <Button variant="danger" disabled={busy} mix={on('click', () => void resetClock())}>
                        Yes, reset the clock check
                      </Button>
                      <Button mix={on('click', () => ((confirmReset = false), void handle.update()))}>Do not reset</Button>
                    </div>
                  ) : (
                    <div>
                      <Button icon={RotateCcw} disabled={busy} mix={on('click', () => ((confirmReset = true), void handle.update()))}>
                        Reset the clock check
                      </Button>
                    </div>
                  )}
                </div>
              ) : null}
              {clockDone ? <Done testId="license-clock-done">{clockDone}</Done> : null}
              {v.readOnly ? (
                <Callout tone="err" icon={Lock} testId="license-readonly" title="Drydock is read-only">
                  <p>Nothing has been deleted. Importing a valid license turns work back on; workflows that were parked resume from their checkpoints.</p>
                  <div mix={cols}>
                    <div>
                      <h3>Still available</h3>
                      <ul>
                        {READONLY_USABLE.map((x) => (
                          <li key={x}>{x}</li>
                        ))}
                      </ul>
                    </div>
                    <div>
                      <h3>Turned off</h3>
                      <ul>
                        {READONLY_OFF.map((x) => (
                          <li key={x}>{x}</li>
                        ))}
                      </ul>
                    </div>
                  </div>
                </Callout>
              ) : null}
            </Group>
          ) : null}

          {v && v.configured ? (
            <Group
              title="Import a license"
              icon={FileKey}
              description="Paste the license token or the path of a license file. The token is sent once to your local core and never shown again. Importing is always allowed."
            >
              <form
                mix={[formCss, on('submit', (e) => (e.preventDefault(), void submit()))]}
                autocomplete="off"
                aria-label="Import a license"
              >
                <Field label="License token">
                  {(id) => (
                    <input
                      id={id}
                      type="password"
                      autocomplete="off"
                      spellcheck={false}
                      name="license-token"
                      mix={ref((n: HTMLInputElement) => {
                        tokenEl = n;
                      })}
                    />
                  )}
                </Field>
                <Field label="Or a license file path">
                  {(id) => (
                    <input
                      id={id}
                      mix={[mono, on('input', (e) => ((path = e.currentTarget.value), void handle.update()))]}
                      value={path}
                      placeholder="C:\Users\you\Downloads\drydock.drydock-license"
                      spellcheck={false}
                    />
                  )}
                </Field>
                <div mix={formActions}>
                  <Button type="submit" variant="primary" disabled={busy}>
                    {act.busy === 'import' ? 'Checking...' : 'Import license'}
                  </Button>
                </div>
              </form>
              {done ? <Done testId="license-done">{done}</Done> : null}
            </Group>
          ) : null}

          {v && v.configured && v.state !== 'none' ? (
            <Group title="Danger zone" icon={TriangleAlert} description="Removing the license turns work off until a license is imported again. Nothing is deleted.">
              <DangerZone>
                {confirmClear ? (
                  <div mix={confirmRow}>
                    <span>Remove the installed license? Drydock becomes read-only until you import one. Nothing is deleted.</span>
                    <Button variant="danger" disabled={busy} mix={on('click', () => void clear())}>
                      Yes, remove it
                    </Button>
                    <Button mix={on('click', () => ((confirmClear = false), void handle.update()))}>Keep it</Button>
                  </div>
                ) : (
                  <Row label="Remove the license" description="Drydock becomes read-only. Your projects, history and backups stay.">
                    <Button variant="danger" icon={Trash2} mix={on('click', () => ((confirmClear = true), void handle.update()))}>
                      Remove the installed license
                    </Button>
                  </Row>
                )}
              </DangerZone>
            </Group>
          ) : null}

          <p mix={intro}>
            Checked offline on this computer; Drydock never contacts a license server. Not tamper-proof.
          </p>
        </div>
      </Page>
    );
  };
}

import { useEffect, useState } from 'react';
import type { AppInfo } from '../api/types';
import { useAction } from '../lib/useAction';
import { useLive } from '../state/hooks';
import { ErrorNote, Page } from './common';
import { TelemetryPanel } from './Telemetry';
import { useClient, useServices, useStoreSnapshot } from '../state/context';
import type { ThemeChoice } from '../lib/theme';

/** docs/ui.md section 4: features that do not exist yet, with the slice that owns them. Keep in step with that table. */
export const NOT_BUILT: { feature: string; slice: string; meanwhile: string }[] = [
  { feature: 'Repo map view', slice: 'gated by decision D5', meanwhile: 'Absent until the Windows CI job (tree-sitter with CGO) is green, per decision D5.' },
  { feature: 'Live verification against vendor accounts (Linear, Notion, Sentry, Aikido)', slice: 'Slice 5', meanwhile: 'Token and OAuth sign-in are built and tested against protocol fixtures only; no live vendor account has been used.' },
  { feature: 'Cloud sync, accounts, team sharing', slice: 'never', meanwhile: 'Drydock has no cloud component.' },
];

/** The environment agent programs start with: an allowlist, plus variable NAMES the human chose to pass on. Never values. */
export function AgentEnvSection() {
  const client = useClient();
  const q = useLive(() => client.agentEnvGet(), [], (e) => e.type === 'agentenv.changed');
  const act = useAction();
  const [text, setText] = useState('');
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    if (q.data) setText(q.data.pass.join('\n'));
  }, [q.data]);
  const names = text.split('\n').map((l) => l.trim()).filter(Boolean);
  return (
    <section aria-label="Agents' environment" data-testid="agent-env">
      <h2>What agents inherit</h2>
      <p>
        Agent programs start with a trimmed environment, not everything Drydock was started with. A token another tool left in your environment (GitHub, a cloud provider) is not
        handed to an agent that a prompt could ask to print it. Claude Code keeps everything named <code>CLAUDE_*</code> or <code>ANTHROPIC_*</code>, so its own sign-in keeps working.
      </p>
      <ErrorNote error={q.error ?? act.error} what="Environment" />
      <form
        className="form"
        onSubmit={async (e) => {
          e.preventDefault();
          setSaved(false);
          const r = await act.run('save', () => client.agentEnvSet(names));
          if (r) {
            setSaved(true);
            q.reload();
          }
        }}
      >
        <label>
          Also pass these variables to agents (names only, one per line; a value is refused)
          <textarea value={text} onChange={(e) => { setText(e.target.value); setSaved(false); }} rows={3} spellCheck={false} placeholder={'AWS_PROFILE\nCLAUDE_CODE_USE_BEDROCK'} />
        </label>
        <div className="form-actions">
          <button type="submit" className="primary" disabled={act.busy !== ''}>
            Save
          </button>
          {saved ? <span role="status">Saved. Applies from the next agent start.</span> : null}
        </div>
      </form>
      <details>
        <summary>What every agent keeps</summary>
        <p className="muted">
          {(q.data?.kept ?? []).join(', ')}; and every variable starting with {(q.data?.prefixes ?? []).join(' or ')} for Claude Code.
        </p>
      </details>
    </section>
  );
}

/** Honest placeholder: there is nothing to click. The update flow is configured when the app is built and has never run on Windows. */
export function UpdatesSection() {
  return (
    <section aria-label="Updates" data-testid="updates-section">
      <h2>Updates</h2>
      <p>
        <strong>Updates are configured when Drydock is built, not in this screen.</strong> This page cannot check for, download or install an update, and there is no
        setting here to change that.
      </p>
      <p className="muted">
        The planned flow uses the Wails updater plus Drydock&apos;s own policy: a backup before every update, and a rollback if the new version does not start. That
        flow has <strong>not been run on Windows</strong>, so treat any update as untested until it has. Your license decides which update channels you may use; see the{' '}
        <a href="#/license">License</a> page. You can make a backup yourself at any time on the <a href="#/backups">Backups</a> page.
      </p>
    </section>
  );
}

export function SettingsView({ theme, onTheme, info }: { theme: ThemeChoice; onTheme: (t: ThemeChoice) => void; info: AppInfo | undefined }) {
  const { transportName } = useServices();
  const snap = useStoreSnapshot();
  return (
    <Page title="Settings and About">
      <h2>Appearance</h2>
      <fieldset className="theme-choice">
        <legend>Theme</legend>
        {(['system', 'light', 'dark'] as ThemeChoice[]).map((t) => (
          <label key={t} className="inline">
            <input type="radio" name="theme" checked={theme === t} onChange={() => onTheme(t)} /> {t[0]!.toUpperCase() + t.slice(1)}
          </label>
        ))}
      </fieldset>
      <h2>About this session</h2>
      <dl className="kv">
        <dt>Transport</dt>
        <dd>{transportName}</dd>
        <dt>Event stream</dt>
        <dd data-testid="stream-status">
          {snap.status}, last seq {snap.lastSeq}
        </dd>
        <dt>You are</dt>
        <dd>{info?.human ?? 'unknown'}</dd>
        <dt>Data location</dt>
        <dd>
          <code>{info?.dataDir ?? 'unknown'}</code>
        </dd>
      </dl>
      <AgentEnvSection />
      <TelemetryPanel />
      <UpdatesSection />
      <h2>Not built yet</h2>
      <p className="muted">
        These features are not implemented. They are listed here instead of shown as disabled screens or sample data.
      </p>
      <table className="table" aria-label="Not built yet">
        <thead>
          <tr>
            <th scope="col">Feature</th>
            <th scope="col">Slice</th>
            <th scope="col">Meanwhile</th>
          </tr>
        </thead>
        <tbody>
          {NOT_BUILT.map((r) => (
            <tr key={r.feature}>
              <td>{r.feature}</td>
              <td>{r.slice}</td>
              <td>{r.meanwhile}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Page>
  );
}

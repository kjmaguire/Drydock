import type { AppInfo } from '../api/types';
import { Page } from './common';
import { TelemetryPanel } from './Telemetry';
import { useServices, useStoreSnapshot } from '../state/context';
import type { ThemeChoice } from '../lib/theme';

/** docs/ui.md section 4: features that do not exist yet, with the slice that owns them. Keep in step with that table. */
export const NOT_BUILT: { feature: string; slice: string; meanwhile: string }[] = [
  { feature: 'Markdown minutes and ADR-candidate export', slice: 'unassigned', meanwhile: 'Absent.' },
  { feature: 'Repo map view', slice: 'gated by decision D5', meanwhile: 'Absent until the Windows CI job is green.' },
  { feature: 'Workflow graph and checkpoint timeline', slice: 'Slice 1C', meanwhile: 'The task view shows status, evidence, reviews, usage and routing decisions only.' },
  { feature: 'Monaco diff, terminal and logs dock', slice: 'Slice 1B', meanwhile: 'A plain unified diff is shown in the task view; there is no terminal.' },
  { feature: 'Context manifest viewer and run explanation', slice: 'Slice 1D', meanwhile: 'The Agent Inspector shows runs and usage; there is no manifest view.' },
  { feature: 'Agent turns chosen by the model router', slice: 'Slice 2 (partly)', meanwhile: 'The router records a decision for every turn, but agents still run with their configured model; the explanation says "static" when it was not routed.' },
  { feature: 'Provider detail panels (quota ledger, OpenRouter key limits), editing a provider, editing prices', slice: 'Slice 1A', meanwhile: 'Providers can be added, tested, enabled, disabled and deleted; the model catalog is read-only.' },
  { feature: 'OAuth sign-in for integrations; live vendor verification', slice: 'Slice 5', meanwhile: 'Integrations use a pasted token and were never run against a live vendor account.' },
  { feature: 'Pull request variants of the workflow templates', slice: 'Slice 4', meanwhile: 'Templates end in a local merge; a project in pull request mode uses the default workflow.' },
  { feature: 'Mentions autocomplete, slash commands, attachments', slice: 'Slice 1A', meanwhile: 'The composer posts plain messages.' },
  { feature: 'Command palette (Ctrl+K)', slice: 'Slice 1A', meanwhile: 'Not built.' },
  { feature: 'Cloud sync, accounts, team sharing', slice: 'never', meanwhile: 'Drydock has no cloud component.' },
];

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

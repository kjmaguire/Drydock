import type { AppInfo } from '../api/types';
import { Page } from './common';
import { useServices, useStoreSnapshot } from '../state/context';
import type { ThemeChoice } from '../lib/theme';

/** docs/ui.md section 4: features that do not exist yet, with the slice that owns them. Keep in step with that table. */
export const NOT_BUILT: { feature: string; slice: string; meanwhile: string }[] = [
  { feature: 'Model Router, "Why this model?", routing decisions, escalation records', slice: 'Slice 2', meanwhile: 'Not routed. The model is whatever team setup chose.' },
  { feature: 'Budgets (warn at 80%, pause at 100%) and per-run ceilings', slice: 'Slice 2 (proposal)', meanwhile: 'Usage is shown; no limit controls exist.' },
  { feature: 'Secrets check (gitleaks)', slice: 'Slice 2', meanwhile: 'The Secrets tool member does not post.' },
  { feature: 'deps, e2e and preview checks', slice: 'unassigned', meanwhile: 'Absent.' },
  { feature: 'Expert Packs, Requirements, Skills, Memory UI', slice: 'Slice 3', meanwhile: 'Absent.' },
  { feature: 'Autonomous multi-agent development, moderator and auto room modes, group chats', slice: 'Slice 4 (proposal)', meanwhile: 'Absent.' },
  { feature: 'Linear, Notion, Sentry and Aikido integrations', slice: 'Slice 5', meanwhile: 'Absent.' },
  { feature: 'Milestones; Markdown minutes and ADR-candidate export', slice: 'unassigned', meanwhile: 'Absent.' },
  { feature: 'Repo map view', slice: 'gated by decision D5', meanwhile: 'Absent until the Windows CI job is green.' },
  { feature: 'Workflow graph and checkpoint timeline', slice: 'Slice 1C', meanwhile: 'The task view shows status, evidence and reviews only.' },
  { feature: 'Monaco diff, terminal and logs dock', slice: 'Slice 1B', meanwhile: 'A plain unified diff is shown in the task view; there is no terminal.' },
  { feature: 'Agent Inspector, context manifest viewer, run explanation', slice: 'Slices 1A / 1D', meanwhile: 'Presence and the Team page only.' },
  { feature: 'Connections page with Log in, Test connection and conformance history; Providers settings', slice: 'Slice 1A', meanwhile: 'The Team page lists detected adapters and their verified capabilities; nothing else about connections.' },
  { feature: 'Threads, direct messages with an agent, mentions autocomplete, slash commands, attachments', slice: 'Slice 1A', meanwhile: 'The composer posts plain messages to the channel.' },
  { feature: 'Command palette (Ctrl+K)', slice: 'Slice 1A', meanwhile: 'Not built.' },
];

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

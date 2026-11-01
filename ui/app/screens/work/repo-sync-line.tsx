// A project's sync line (decision P143), under the crew rail's figures on Work: where the folder stands against its remote, one
// line, with Sync now (fetch, then a fast-forward only when nothing else changes; the core decides, ext_reposync.go). It shows
// only for a repository with an origin remote. Reading the state when the last check is old starts a fetch-only check in the
// core (the background check); repo.synced brings the answer.

import { css, on, type Handle } from 'remix/component';
import { RefreshCw } from 'lucide';
import { action, query, services } from '../../state/services.tsx';
import { IconButton } from '../../ui/button.tsx';
import { t } from '../../ui/tokens.ts';
import { remoteWord, showsSync, syncLine } from './repo-sync.ts';

const lineCss = css({
  display: 'grid',
  gap: '2px',
  padding: `0 ${t.s1}`,
  '& .row': { display: 'flex', alignItems: 'center', gap: t.s2, minHeight: '28px', color: t.textMuted, fontSize: t.textSm },
  '& .state': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .state[data-tone="ok"]': { color: t.text },
  '& .state[data-tone="behind"]': { color: t.text, fontWeight: '500' },
  '& .state[data-tone="bad"]': { color: t.danger },
  '& .why': { margin: '0', color: t.textFaint, fontSize: t.textXs },
  '& button': { flex: 'none', width: '26px', height: '26px', minHeight: '0' },
});

export function RepoSyncLine(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const act = action(handle);
  const st = query(handle, {
    key: () => handle.props.projectId,
    load: () => s.client.repoSyncState(handle.props.projectId),
    when: (e) => (e.type === 'repo.synced' && e.projectId === handle.props.projectId) || e.type === 'repo.syncSettings' || e.type === 'project.updated',
  });
  const sync = () =>
    act.run('sync', async () => {
      const r = await s.client.repoSync(handle.props.projectId);
      st.set(r);
    });

  return () => {
    const data = st.data;
    if (!showsSync(data)) return null;
    const line = syncLine(data);
    const busy = !!act.busy;
    const r = remoteWord(data);
    return (
      <div mix={lineCss} data-testid="repo-sync" data-behind={String(data.behind)} data-ahead={String(data.ahead)} data-outcome={data.outcome || ''}>
        <div class="row">
          <span class="state" data-tone={line.tone} data-testid="repo-sync-state" title={data.error || line.text} role="status">
            {busy ? `Syncing with ${r}…` : line.text}
          </span>
          <IconButton icon={RefreshCw} size={14} label="Sync now" title={`Sync now: fetch from ${r}, then update ${data.branch} when nothing else changes`} data-testid="repo-sync-now" disabled={busy} mix={on('click', () => void sync())} />
        </div>
        {act.error ? (
          <p class="why" role="alert" data-testid="repo-sync-why">
            {act.error.message}
          </p>
        ) : line.why ? (
          <p class="why" data-testid="repo-sync-why">
            {line.why}
          </p>
        ) : null}
      </div>
    );
  };
}

// Settings > General, Notifications (decision G1): the person's switches for being told outside the window. On (the core's
// default), the taskbar button asks for attention and a Windows notification shows when an approval, a question or a review comes
// into the Inbox while Drydock is not in front; "Play a sound" (on by default) makes that notification play Windows's sound.
// Inside the window the Inbox count and a toast say it either way.

import { type Handle } from 'remix/component';
import { Bell } from 'lucide';
import type { NotifyPrefs } from '../../api/notifyClient.ts';
import { action, query, retryFor, services } from '../../state/services.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Switch } from '../../ui/form.tsx';
import { Group, panel } from './settings-kit.tsx';

export function NotifySection(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.notifyGet(), when: (e) => e.type === 'notify.changed' });
  const act = action(handle);
  const set = async (next: Partial<NotifyPrefs>) => {
    const cur = q.data;
    if (!cur) return;
    const r = await act.run('set', () => s.client.notifySet({ ...cur, ...next }));
    if (r) q.set(r);
  };
  return () => (
    <Group
      title="Notifications"
      icon={Bell}
      label="Notifications"
      testId="notify"
      description="When Drydock is behind and something needs you, the taskbar button lights up and Windows shows a notice naming the request kind, project and task, never what was asked."
    >
      <ErrorNote error={q.error ?? act.error} retry={retryFor(q, act)} what="Notifications" />
      {!q.data && q.loading ? <Loading /> : null}
      {q.data ? (
        <div mix={panel}>
          <Switch label="Tell me when something needs me" checked={q.data.needsYou} disabled={act.busy !== ''} onChange={(v) => void set({ needsYou: v })} />
          <Switch label="Play a sound" checked={q.data.sound} disabled={act.busy !== '' || !q.data.needsYou} onChange={(v) => void set({ sound: v })} />
        </div>
      ) : null}
    </Group>
  );
}

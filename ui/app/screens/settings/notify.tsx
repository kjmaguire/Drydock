// Settings > General, Notifications (decision G1): the person's switch for being told outside the window. On (the core's
// default), the taskbar button asks for attention when an approval, a question or a review comes into the Inbox while Drydock is
// not in front. Inside the window the Inbox count and a toast say it either way.

import { type Handle } from 'remix/component';
import { Bell } from 'lucide';
import { action, query, retryFor, services } from '../../state/services.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Switch } from '../../ui/form.tsx';
import { Group, panel } from './settings-kit.tsx';

export function NotifySection(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.notifyGet(), when: (e) => e.type === 'notify.changed' });
  const act = action(handle);
  const set = async (needsYou: boolean) => {
    const r = await act.run('set', () => s.client.notifySet({ needsYou }));
    if (r) q.set(r);
  };
  return () => (
    <Group
      title="Notifications"
      icon={Bell}
      label="Notifications"
      testId="notify"
      description="When Drydock is not in front and something new needs you, its taskbar button lights up. Nothing leaves this computer."
    >
      <ErrorNote error={q.error ?? act.error} retry={retryFor(q, act)} what="Notifications" />
      {!q.data && q.loading ? <Loading /> : null}
      {q.data ? (
        <div mix={panel}>
          <Switch label="Tell me when something needs me" checked={q.data.needsYou} disabled={act.busy !== ''} onChange={(v) => void set(v)} />
        </div>
      ) : null}
    </Group>
  );
}

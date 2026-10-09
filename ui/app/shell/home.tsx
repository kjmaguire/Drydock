// "/": open the selected project's #general (else its first channel) as soon as the rooms are known.

import { navigate, type Handle } from 'remix/component';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { Loading } from '../ui/feedback.tsx';

export function HomeScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.project);
  let sent = false;
  return () => {
    const channels = s.project.roomList.filter((r) => r.kind === 'channel' && !r.archived);
    const first = channels.find((r) => r.name === 'general') ?? channels[0];
    if (first && !sent) {
      sent = true;
      handle.queueTask(() => void navigate(routes.room.href({ id: first.id }), { history: 'replace' }));
    }
    return <Loading label="Opening the project channel..." />;
  };
}

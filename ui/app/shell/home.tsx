// "/": open the selected project's first channel as soon as the rooms are known.

import { navigate, type Handle } from 'remix/component';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { Loading } from '../ui/feedback.tsx';

export function HomeScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.project);
  let sent = false;
  return () => {
    const first = s.project.roomList.find((r) => r.kind === 'channel');
    if (first && !sent) {
      sent = true;
      handle.queueTask(() => void navigate(routes.room.href({ id: first.id }), { history: 'replace' }));
    }
    return <Loading label="Opening the project channel..." />;
  };
}

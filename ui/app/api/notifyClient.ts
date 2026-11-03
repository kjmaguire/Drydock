// Being told outside the window (internal/core/ext_notify.go, decision G1): the person's switch, kept by the core. Like the rest
// of the client: one function per method in the registry.

import type { Transport } from './transport.ts';

/**
 * notify.get and notify.set: whether the taskbar button asks for attention and a Windows notification shows when something new
 * needs the person, and whether that notification plays a sound.
 */
export interface NotifyPrefs {
  needsYou: boolean;
  sound: boolean;
}

export interface NotifyClient {
  notifyGet(): Promise<NotifyPrefs>;
  notifySet(prefs: NotifyPrefs): Promise<NotifyPrefs>;
}

// a core that answers nothing kept: on, as the core's own defaults
const clean = (v: Partial<NotifyPrefs> | null | undefined): NotifyPrefs => ({ needsYou: v?.needsYou ?? true, sound: v?.sound ?? true });

export function createNotifyClient(t: Transport): NotifyClient {
  return {
    notifyGet: async () => clean(await t.call<NotifyPrefs | null>('notify.get', {})),
    notifySet: async (prefs) => clean(await t.call<NotifyPrefs | null>('notify.set', prefs)),
  };
}

// The person's keyboard shortcuts (internal/core/ext_keys.go, decision P132): the bindings that differ from the defaults in
// lib/keys.ts KEYMAP, kept by the core for the workspace. Like the rest of the client: one function per method in the registry.

import type { Transport } from './transport.ts';

/** keys.get and keys.set: the shortcut ids the person bound to other keys, each with its combination ("Ctrl+Shift+K"). */
export interface KeyBindings {
  bindings: Record<string, string>;
}

export interface KeysClient {
  keysGet(): Promise<KeyBindings>;
  /**
   * Replaces the bindings with these (an id left out, or bound to its default, goes back to its default; {} resets them all). The
   * core refuses an unknown id, a combination the map cannot name or without Ctrl or Alt, one the window or the fields keep, and
   * two shortcuts on one combination, each with a message that says which.
   */
  keysSet(bindings: Record<string, string>): Promise<KeyBindings>;
}

const clean = (v: Partial<KeyBindings> | null | undefined): KeyBindings => ({ bindings: { ...(v?.bindings ?? {}) } });

export function createKeysClient(t: Transport): KeysClient {
  return {
    keysGet: async () => clean(await t.call<KeyBindings | null>('keys.get', {})),
    keysSet: async (bindings) => clean(await t.call<KeyBindings | null>('keys.set', { bindings })),
  };
}

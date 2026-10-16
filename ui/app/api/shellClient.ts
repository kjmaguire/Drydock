// The window's shell (internal/core/ext_shell.go, audit 2026-10-16, M23): what the page cannot do for itself inside the desktop
// window. Like the rest of the client: one function per method in the core registry.

import type { Transport } from './transport.ts';

export interface ShellClient {
  /**
   * Asks the shell to open a web (http, https) or mail (mailto) address in the person's own browser or mail program. The core
   * refuses any other address, a web address with a password in it, and a window that cannot open another program, each with a
   * message that does not repeat the address. Resolves when the shell has handed the address over.
   */
  shellOpenExternal(url: string): Promise<void>;
}

export function createShellClient(t: Transport): ShellClient {
  return {
    shellOpenExternal: async (url) => {
      await t.call('shell.openExternal', { url });
    },
  };
}

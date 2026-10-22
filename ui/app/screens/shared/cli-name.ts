// The name the app gives an install of an agent CLI, in one place: the Connections page's tiles and cards, the chats' CLI
// pickers (new chat, a chat's properties) and a chat's header. internal/connections/name.go CLIName is the same rule for what the
// core writes (a moved chat's note, the sign-in terminal's title); internal/connections/testdata/cli_names.json holds the cases
// both sides' tests pass, so the UI and the core cannot drift apart.

import type { Chat } from '../../api/chatClient.ts';
import type { Connection } from '../../api/types.ts';

/** The product name of each built-in adapter (name.go productNames, the same table). */
const PRODUCT_NAMES: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', gemini: 'Gemini CLI', cline: 'Cline', opencode: 'OpenCode', goose: 'Goose' };

/** "Claude Code" for the claude adapter, the product name of the other built-in ones, else the adapter id. */
export function adapterLabel(id: string): string {
  return PRODUCT_NAMES[id] ?? id;
}

/** One install of an agent CLI: on Windows, or inside a WSL distro (run mode wsl). A Connection is one. */
export type CliInstall = Pick<Connection, 'adapter' | 'runMode' | 'distro'> & Partial<Pick<Connection, 'installed' | 'status'>>;

const inWSL = (c: Pick<CliInstall, 'runMode'>): boolean => c.runMode === 'wsl';

/** The CLI was found there (an install whose state is not given counts as one). */
const found = (c: CliInstall): boolean => c.installed !== false && c.status !== 'not_installed';

/**
 * The same CLI is installed in another WSL distro too (`all`: the installs the app knows, the Connections page's list), so the
 * distro is what tells this one apart.
 */
export const wslTwins = (c: CliInstall, all: readonly CliInstall[]): boolean =>
  inWSL(c) && all.some((o) => inWSL(o) && o.adapter === c.adapter && o.distro !== c.distro && found(o));

/**
 * An install's name: the CLI's product name ("Claude Code") on Windows, "Claude Code WSL" inside a WSL distro, without the
 * distro (the owner: "we don't need to see the Ubuntu"; where it is shown, a Runs row or a tooltip names it). Only when the
 * same CLI is installed in another distro too (wslTwins) does the name carry the distro: "Claude Code WSL (Debian)".
 */
export function cliName(c: CliInstall, all: readonly CliInstall[] = []): string {
  if (!inWSL(c)) return adapterLabel(c.adapter);
  return wslTwins(c, all) ? `${adapterLabel(c.adapter)} WSL (${c.distro ?? ''})` : `${adapterLabel(c.adapter)} WSL`;
}

/** The install a chat runs with: its adapter, inside its WSL distro or on Windows. */
export const chatInstall = (c: Pick<Chat, 'adapter' | 'wslDistro'>): CliInstall =>
  c.wslDistro ? { adapter: c.adapter, runMode: 'wsl', distro: c.wslDistro } : { adapter: c.adapter, runMode: 'native' };

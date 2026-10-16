// xterm.js for one of the human's terminals (docs/ui.md 6.5). Imported lazily by widgets.ts. Its colors are Drydock's
// tokens, read from the computed styles and re-read when the theme changes.
import { FitAddon } from '@xterm/addon-fit';
import { Terminal, type ITheme } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { onThemeChange } from '../../lib/effectiveTheme.ts';
import { withAlpha } from './dock-format.ts';
import { readColors, type TerminalFactory } from './widgets.ts';

function theme(): ITheme {
  const c = readColors({
    background: '--bg-app',
    foreground: '--text',
    accent: '--accent',
    muted: '--text-muted',
    red: '--danger',
    green: '--success',
    yellow: '--warning',
    blue: '--info',
  });
  return {
    background: c.background,
    foreground: c.foreground,
    cursor: c.foreground,
    cursorAccent: c.background,
    selectionBackground: withAlpha(c.accent, 0.35) ?? c.accent,
    red: c.red,
    brightRed: c.red,
    green: c.green,
    brightGreen: c.green,
    yellow: c.yellow,
    brightYellow: c.yellow,
    blue: c.blue,
    brightBlue: c.blue,
    brightBlack: c.muted,
  };
}

export const createXterm: TerminalFactory = (host) => {
  const font = getComputedStyle(document.documentElement).getPropertyValue('--font-mono').trim();
  const term = new Terminal({
    fontSize: 13,
    fontFamily: font || undefined,
    cursorBlink: true,
    scrollback: 5000,
    convertEol: false,
    theme: theme(),
    screenReaderMode: false,
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(host);
  const offTheme = onThemeChange(() => {
    term.options.theme = theme();
  });
  const subs: { dispose(): void }[] = [];
  return {
    get cols() {
      return term.cols;
    },
    get rows() {
      return term.rows;
    },
    write: (d) => term.write(d),
    onData: (fn) => void subs.push(term.onData(fn)),
    fit: () => fit.fit(),
    focus: () => term.focus(),
    dispose: () => {
      offTheme();
      for (const s of subs.splice(0)) s.dispose();
      term.dispose();
    },
  };
};

// xterm.js over one terminal of the core (docs/ui.md 6.5). Loaded on first use. Output is read with a long poll by offset and
// written straight into xterm (never through React state); input and resizes go back as calls.
import { FitAddon } from '@xterm/addon-fit';
import { Terminal, type ITheme } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef } from 'react';
import type { DockClient } from '../api/dockClient';
import { isDarkTheme, onThemeChange } from '../lib/effectiveTheme';

const DARK: ITheme = { background: '#121418', foreground: '#eceef2', cursor: '#eceef2', selectionBackground: '#35507a' };
const LIGHT: ITheme = { background: '#ffffff', foreground: '#15171c', cursor: '#15171c', selectionBackground: '#b7cdf5' };

export interface XtermProps {
  client: Pick<DockClient, 'terminalRead' | 'terminalWrite' | 'terminalResize'>;
  id: string;
  onExit(code: number): void;
}

export default function Xterm({ client, id, onExit }: XtermProps) {
  const host = useRef<HTMLDivElement>(null);
  const exitRef = useRef(onExit);
  exitRef.current = onExit;

  useEffect(() => {
    if (!host.current) return;
    const term = new Terminal({ fontSize: 13, cursorBlink: true, scrollback: 5000, convertEol: false, theme: isDarkTheme() ? DARK : LIGHT, screenReaderMode: false });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host.current);
    let alive = true;
    const offTheme = onThemeChange((dark) => {
      term.options.theme = dark ? DARK : LIGHT;
    });
    const data = term.onData((s) => {
      void client.terminalWrite(id, s).catch(() => {});
    });
    const resize = () => {
      try {
        fit.fit();
      } catch {
        return; // not laid out yet
      }
      void client.terminalResize(id, term.cols, term.rows).catch(() => {});
    };
    const ro = new ResizeObserver(resize);
    ro.observe(host.current);
    resize();
    term.focus();

    (async () => {
      let next = 0;
      while (alive) {
        try {
          const c = await client.terminalRead(id, next, 15000);
          if (!alive) return;
          if (c.dropped > 0) term.write(`\r\n[${c.dropped} bytes of earlier output were not kept]\r\n`);
          if (c.data.length) term.write(c.data);
          next = c.next;
          if (c.exited && c.data.length === 0) {
            term.write(`\r\n[the shell exited with code ${c.exitCode}]\r\n`);
            exitRef.current(c.exitCode);
            return;
          }
        } catch (e) {
          if (!alive) return;
          term.write(`\r\n[the terminal is gone: ${e instanceof Error ? e.message : String(e)}]\r\n`);
          return;
        }
      }
    })();

    return () => {
      alive = false;
      offTheme();
      data.dispose();
      ro.disconnect();
      term.dispose();
    };
  }, [client, id]);

  return <div ref={host} className="xterm-host" data-testid="xterm" />;
}

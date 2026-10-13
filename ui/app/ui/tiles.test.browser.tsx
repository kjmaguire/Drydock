import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { Server } from 'lucide';
import { cleanup, renderPlain } from '../test/render.tsx';
import { Tile, tileGrid } from './tiles.tsx';

afterEach(cleanup);

describe('Tile', () => {
  it('shows a brand logo, the name, what it is and its state, and reports clicks and the selection', () => {
    let clicks = 0;
    const { container } = renderPlain(
      <ul mix={tileGrid}>
        <Tile title="Linear" logo="linear" blurb="Issues and projects" state="Not connected" selected onSelect={() => clicks++} testId="t" data={{ kind: 'linear' }} />
      </ul>,
    );
    const b = container.querySelector('[data-testid="t"]') as HTMLButtonElement;
    expect(b.getAttribute('aria-pressed')).toBe('true');
    expect(b.getAttribute('data-kind')).toBe('linear');
    expect(b.querySelector('[data-brand="linear"]')).not.toBeNull();
    expect(b.textContent).toBe('LinearIssues and projectsNot connected');
    b.click();
    expect(clicks).toBe(1);
  });

  it('shows an icon on a neutral square when the thing has no brand, and colours the state by its tone', () => {
    // The plain test page has no theme stylesheet; define the one colour this checks.
    document.documentElement.style.setProperty('--success', 'rgb(1, 128, 2)');
    const { container } = renderPlain(
      <ul>
        <Tile title="My server" logo="not-a-brand" icon={Server} state="Ready" tone="ok" />
      </ul>,
    );
    const b = container.querySelector('button') as HTMLButtonElement;
    expect(b.getAttribute('aria-pressed')).toBe('false');
    expect(b.querySelector('[data-brand]')).toBeNull();
    expect(b.querySelector('svg')).not.toBeNull();
    const state = b.querySelector('small[data-tone="ok"]') as HTMLElement;
    expect(state.textContent).toBe('Ready');
    expect(getComputedStyle(state).color).toBe('rgb(1, 128, 2)');
    document.documentElement.style.removeProperty('--success');
  });

  it('is draggable only when it takes a drag, reports the drag and dims while dragged', () => {
    const seen: string[] = [];
    const { container } = renderPlain(
      <ul>
        <Tile title="Plain" testId="plain" />
        <Tile title="Moves" testId="moves" dragging onDragStart={(e) => seen.push(`start:${e.type}`)} onDragEnd={(e) => seen.push(`end:${e.type}`)} />
      </ul>,
    );
    const plain = container.querySelector('[data-testid="plain"]') as HTMLButtonElement;
    expect(plain.hasAttribute('draggable')).toBe(false);
    expect(plain.hasAttribute('data-dragging')).toBe(false);
    const moves = container.querySelector('[data-testid="moves"]') as HTMLButtonElement;
    expect(moves.getAttribute('draggable')).toBe('true');
    expect(moves.getAttribute('data-dragging')).toBe('true');
    moves.dispatchEvent(new DragEvent('dragstart', { bubbles: true, dataTransfer: new DataTransfer() }));
    moves.dispatchEvent(new DragEvent('dragend', { bubbles: true }));
    expect(seen).toEqual(['start:dragstart', 'end:dragend']);
  });

  it('keeps a long blurb and state inside the tile, wrapped, under the app stylesheet that makes every other button one line', () => {
    // the test page carries no stylesheet (the runner serves only scripts); this is app.css's rule for every button
    const style = document.createElement('style');
    style.textContent = '@layer base, rmx, app; @layer base { button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 30px; padding: 0 12px; white-space: nowrap; } }';
    document.head.append(style);
    const blurb = 'Official Cloudflare documentation for building email sending and routing integrations with Workers, Email Routing and Email Service.';
    const { container } = renderPlain(
      <div style={{ width: '460px' }}>
        <ul mix={tileGrid}>
          <Tile title="cloudflare-email-service" logo="cloudflare" blurb={blurb} state="Accepted ✓ · whole team · synced 2026-10-12, 11:02" tone="ok" testId="t" />
          <Tile title="k2" logo="cloudflare" blurb="short" state="Accepted ✓" tone="ok" testId="u" />
        </ul>
      </div>,
    );
    const b = container.querySelector('[data-testid="t"]') as HTMLButtonElement;
    const box = b.getBoundingClientRect();
    const logo = (b.firstElementChild as HTMLElement).getBoundingClientRect();
    const text = (b.lastElementChild as HTMLElement).getBoundingClientRect();
    expect(getComputedStyle(b).whiteSpace).toBe('normal');
    expect(logo.left).toBeGreaterThanOrEqual(box.left);
    expect(text.right).toBeLessThanOrEqual(box.right + 0.5);
    expect(text.left).toBeGreaterThanOrEqual(logo.right);
    // wrapped: the text block is taller than one line of 12px small text
    expect(text.height).toBeGreaterThan(60);
    const u = (container.querySelector('[data-testid="u"]') as HTMLElement).getBoundingClientRect();
    expect(u.left).toBeGreaterThan(box.left); // two tiles side by side in a 460px grid
    style.remove();
  });
});

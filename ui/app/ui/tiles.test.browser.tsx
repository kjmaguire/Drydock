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
});

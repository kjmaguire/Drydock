import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, renderPlain } from '../test/render.tsx';
import { BrandLogo, brandKey, brandTitle } from './brand-logo.tsx';

afterEach(cleanup);

describe('BrandLogo', () => {
  it('maps integration, MCP catalog and agent CLI ids to one mark per brand', () => {
    expect(brandKey('gh')).toBe('github');
    expect(brandKey('github')).toBe('github');
    expect(brandKey('sentryx')).toBe('sentry');
    expect(brandKey('cfdocs')).toBe('cloudflare');
    expect(brandKey('ctx7')).toBe('context7');
    expect(brandKey('mslearn')).toBe('microsoft');
    expect(brandKey('aikido')).toBe('aikido');
    expect(brandKey('my-own-server')).toBe('');
    expect(brandTitle('ctx7')).toBe('Context7');
    expect(brandTitle('linear')).toBe('Linear');
  });

  it('draws every mark, labelled only when asked, and nothing for an unknown id', () => {
    for (const id of ['linear', 'notion', 'sentry', 'aikido', 'github', 'cloudflare', 'mslearn', 'ctx7', 'claude', 'gemini', 'opencode', 'cline']) {
      const { container } = renderPlain(<BrandLogo id={id} label />);
      const tile = container.querySelector('[data-brand]') as HTMLElement;
      expect(tile).not.toBeNull();
      expect(tile.getAttribute('aria-label')).toBe(brandTitle(id));
      expect(tile.querySelector('svg path, svg rect')).not.toBeNull();
      expect(tile.getBoundingClientRect().width).toBe(24);
    }
    const plain = renderPlain(<BrandLogo id="linear" size={40} />);
    const tile = plain.container.querySelector('[data-brand]') as HTMLElement;
    expect(tile.getAttribute('aria-hidden')).toBe('true');
    expect(tile.getBoundingClientRect().width).toBe(40);
    expect(renderPlain(<BrandLogo id="nope" />).container.querySelector('[data-brand]')).toBeNull();
  });
});

import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, renderPlain } from '../test/render.tsx';
import { BrandLogo, brandKey, brandTitle } from './brand-logo.tsx';

afterEach(cleanup);

describe('BrandLogo', () => {
  it('maps integration, MCP catalog and agent CLI ids to one mark per brand', () => {
    expect(brandKey('gh')).toBe('github');
    expect(brandKey('github')).toBe('github');
    expect(brandKey('cfdocs')).toBe('cloudflare');
    expect(brandKey('ctx7')).toBe('context7');
    expect(brandKey('mslearn')).toBe('microsoft');
    expect(brandKey('aikido')).toBe('aikido');
    expect(brandKey('my-own-server')).toBe('');
    expect(brandTitle('ctx7')).toBe('Context7');
    expect(brandTitle('linear')).toBe('Linear');
  });

  it('knows the model providers and the Linux of a WSL distro, and has nothing for a brand Simple Icons does not carry', () => {
    expect(brandKey('mistralai')).toBe('mistral');
    expect(brandKey('lm-studio')).toBe('lmstudio');
    expect(brandTitle('mistralai')).toBe('Mistral AI');
    expect(brandTitle('openrouter')).toBe('OpenRouter');
    expect(brandTitle('anthropic')).toBe('Anthropic');
    expect(brandTitle('ubuntu')).toBe('Ubuntu');
    // no drawn guesses: these have no Simple Icons entry, so their tiles show an icon
    for (const id of ['openai', 'codex', 'groq', 'goose', 'antigravity', 'custom']) expect(brandKey(id)).toBe('');
  });

  it('draws every mark, labelled only when asked, and nothing for an unknown id', () => {
    const providers = ['anthropic', 'openrouter', 'ollama', 'google', 'mistral', 'deepseek', 'vllm', 'lmstudio'];
    const linux = ['ubuntu', 'debian', 'linux'];
    for (const id of ['linear', 'notion', 'sentry', 'aikido', 'github', 'cloudflare', 'mslearn', 'ctx7', 'claude', 'gemini', 'opencode', 'cline', ...providers, ...linux]) {
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

  it("draws Aikido's own mark (from its official banner), not a lettermark, inside the tile", () => {
    const { container } = renderPlain(<BrandLogo id="aikido" size={48} label />);
    const svg = container.querySelector('[data-brand="aikido"] svg') as SVGSVGElement;
    expect(svg.querySelector('text')).toBeNull();
    const paths = svg.querySelectorAll('path');
    expect(paths.length).toBe(2);
    expect(paths[0]!.closest('g')!.getAttribute('fill')).toBe('#6551F3');
    // the mark (58.185 by 65.278 units in the banner) is scaled into the 24-unit box
    const box = (svg.querySelector('g') as SVGGElement).getBBox();
    expect(Math.round(box.width * 0.36766)).toBe(21);
    expect(Math.round(box.height * 0.36766)).toBe(24);
  });
});

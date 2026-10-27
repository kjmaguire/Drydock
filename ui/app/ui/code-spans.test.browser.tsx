import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, renderPlain } from '../test/render.tsx';
import { codeSpans, sentence } from './code-spans.tsx';

afterEach(cleanup);

describe('codeSpans', () => {
  it('shows a command in backticks as code, without the backticks', () => {
    const { container } = renderPlain(<p>{codeSpans('Install it from https://cli.github.com and run `gh auth login`.')}</p>);
    expect(container.textContent).toBe('Install it from https://cli.github.com and run gh auth login.');
    expect(container.querySelector('code')!.textContent).toBe('gh auth login');
  });

  it('leaves a sentence with an unpaired backtick as it is, and never reads HTML', () => {
    const { container } = renderPlain(<p>{codeSpans('a ` alone <b>x</b>')}</p>);
    expect(container.textContent).toBe('a ` alone <b>x</b>');
    expect(container.querySelector('code, b')).toBeNull();
  });

  it('starts a sentence with a capital', () => {
    expect(sentence('the GitHub CLI (gh) is not installed')).toBe('The GitHub CLI (gh) is not installed');
    expect(sentence('')).toBe('');
  });
});

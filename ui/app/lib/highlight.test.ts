import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { familyOf, highlight, HIGHLIGHT_MAX } from './highlight.ts';

const shape = (code: string, lang?: string) => highlight(code, lang).map((t) => `${t.kind}:${t.text}`);

describe('code colouring (P89)', () => {
  it('splits code into runs that join back to exactly the code', () => {
    const code = 'func Add(a, b int) int {\n\t// sum\n\treturn a + b + 0x1F // "not a string"\n}\ns := "a \\" b" + `raw\nlines`';
    const runs = highlight(code, 'go');
    expect(runs.map((r) => r.text).join('')).toBe(code);
    expect(runs.filter((r) => r.kind === 'keyword').map((r) => r.text)).toEqual(['func', 'return']);
    expect(runs.filter((r) => r.kind === 'comment').map((r) => r.text)).toEqual(['// sum', '// "not a string"']);
    expect(runs.filter((r) => r.kind === 'string').map((r) => r.text)).toEqual(['"a \\" b"', '`raw\nlines`']);
    expect(runs.filter((r) => r.kind === 'number').map((r) => r.text)).toEqual(['0x1F']);
  });

  it('knows the families by their usual fence names', () => {
    expect(['ts', 'TSX', 'javascript', 'golang', 'py', 'bash', 'ps1', 'yml', 'c++', 'csharp', 'sql', 'json'].map(familyOf)).toEqual([
      'js', 'js', 'js', 'go', 'python', 'shell', 'powershell', 'yaml', 'c', 'c', 'sql', 'json',
    ]);
    expect(familyOf('brainfuck')).toBe(undefined);
    expect(familyOf('')).toBe(undefined);
  });

  it('reads # comments only at a word boundary, SQL keywords in any case, and an unclosed quote to its line end', () => {
    expect(shape('echo "$#" # done', 'bash')).toEqual(['plain:echo ', 'string:"$#"', 'plain: ', 'comment:# done']);
    expect(shape('SELECT 1 FROM t', 'sql')).toEqual(['keyword:SELECT', 'plain: ', 'number:1', 'plain: ', 'keyword:FROM', 'plain: t']);
    expect(shape("x = 'open\ny", 'python')).toEqual(['plain:x = ', "string:'open", 'plain:\ny']);
    expect(shape('a1 = b2', 'js')).toEqual(['plain:a1 = b2']);
  });

  it('leaves unknown languages and long code plain', () => {
    expect(shape('const x = 1', 'brainfuck')).toEqual(['plain:const x = 1']);
    expect(shape('const x = 1')).toEqual(['plain:const x = 1']);
    const long = 'x'.repeat(HIGHLIGHT_MAX + 1);
    expect(highlight(long, 'js')).toEqual([{ kind: 'plain', text: long }]);
    expect(highlight('', 'js')).toEqual([]);
  });
});

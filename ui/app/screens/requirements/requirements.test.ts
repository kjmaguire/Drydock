import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { criteriaLines, documentProblem, evidenceOutcome, fileProblem, isDocument, isGWT, isReadable, linkCriteria, linkedTaskIds, linkLabel, titleFromFile } from './requirements.ts';

describe('Requirements helpers', () => {
  it('refuses a file over 1 MB before reading it and titles a file by its name', () => {
    expect(fileProblem(1_000_001)).toBe('That file is larger than 1 MB.');
    expect(fileProblem(1_000_000)).toBe('');
    expect(titleFromFile('checkout.md')).toBe('checkout');
    expect(titleFromFile('notes.v2.txt')).toBe('notes.v2');
  });

  it('a PDF or DOCX is read by the core, up to 32 MB', () => {
    expect([isDocument('spec.PDF'), isDocument('spec.docx'), isDocument('spec.md'), isDocument('pdf')]).toEqual([true, true, false, false]);
    expect(documentProblem(32 << 20)).toBe('');
    expect(documentProblem((32 << 20) + 1)).toBe('That file is larger than 32 MB.');
  });

  it('reads documents, Markdown and text, never an image or a spreadsheet as text', () => {
    expect([isReadable('a.pdf'), isReadable('a.DOCX'), isReadable('a.md'), isReadable('a.markdown'), isReadable('a.txt'), isReadable('notes', 'text/plain')]).toEqual([
      true,
      true,
      true,
      true,
      true,
      true,
    ]);
    expect([isReadable('image.png', 'image/png'), isReadable('table.xlsx'), isReadable('slides.pptx', 'application/octet-stream')]).toEqual([false, false, false]);
  });

  it('criteria are one per line, trimmed', () => {
    expect(criteriaLines(' Given a\n\nGiven b \n')).toEqual(['Given a', 'Given b']);
  });

  it('evidence is PASS or FAIL only when the core says so', () => {
    expect(evidenceOutcome({ passed: true }).label).toBe('PASS');
    expect(evidenceOutcome({ passed: false }).label).toBe('FAIL');
    expect(evidenceOutcome({ passed: null }).label).toBe('INFO');
    expect(evidenceOutcome({}).label).toBe('INFO');
  });

  it('lists each linked task once', () => {
    expect(linkedTaskIds([{ taskId: 'a' }, { taskId: 'b' }, { taskId: 'a' }])).toEqual(['a', 'b']);
  });
});

describe('linking criteria (X42)', () => {
  it('takes what the core takes as Given/When/Then', () => {
    expect(isGWT('AC1: Given a, when b, then c')).toBe(true);
    expect(isGWT('given a\nwhen b\nthen c')).toBe(true);
    expect(isGWT('When b, given a, then c')).toBe(false);
    expect(isGWT('Givens are whenever thens')).toBe(false);
  });

  it('sends the ticked criteria and the written ones, each once, and names the refused lines', () => {
    const own = ['Given a, when b, then c', 'Given d, when e, then f'];
    expect(linkCriteria(own, new Set([0, 1]), '')).toEqual({ criteria: own, refused: [] });
    expect(linkCriteria(own, new Set([1]), 'given  D, when e, then F\nfast')).toEqual({ criteria: ['Given d, when e, then f', 'fast'], refused: ['fast'] });
    expect(linkLabel(3, 12)).toBe('Link 3 criteria of #12');
    expect(linkLabel(1, 12)).toBe('Link 1 criterion of #12');
    expect(linkLabel(0, 12)).toBe('Link');
  });
});

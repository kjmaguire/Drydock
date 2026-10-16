import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { classifyAddress } from './external-links.ts';

// audit 2026-10-16, M23: which addresses go to the shell, which the page keeps, which are stopped and never sent.

const BASE = 'https://app.test/tasks/t1';
const OWN = 'https://app.test';
const kind = (raw: string) => classifyAddress(raw, BASE, OWN);

describe('classifyAddress', () => {
  it('sends web and mail addresses outside the app to the shell, normalized', () => {
    expect(kind('https://example.com/docs')).toEqual({ kind: 'external', href: 'https://example.com/docs' });
    expect(kind('http://example.com')).toEqual({ kind: 'external', href: 'http://example.com/' });
    expect(kind('  HTTPS://EXAMPLE.com/Path?q=1#top ')).toEqual({ kind: 'external', href: 'https://example.com/Path?q=1#top' });
    expect(kind('mailto:kyle@example.com')).toEqual({ kind: 'external', href: 'mailto:kyle@example.com' });
    // another port or scheme of the app's own host is another origin
    expect(kind('http://app.test/tasks')).toEqual({ kind: 'external', href: 'http://app.test/tasks' });
    expect(kind('https://app.test:8443/')).toEqual({ kind: 'external', href: 'https://app.test:8443/' });
    // the core decides what an address with a password in it is; the page does not pre-judge web addresses
    expect(kind('https://kyle:secret@example.com/')).toMatchObject({ kind: 'external' });
  });

  it('leaves the app\'s own routes and anchors, and the files the page offers, to the page', () => {
    for (const own of ['/library', 'library', '../tasks', '#main', '', '?filter=x', 'https://app.test/x?y#z', 'blob:https://app.test/0b9c1c8e-1c5e-4a40-9a2b-4f1d3a9b7f10', 'data:text/plain,hello', 'about:blank']) {
      expect(kind(own)).toEqual({ kind: 'own' });
    }
  });

  it('stops every other address and never sends it: scripts, files, protocol handlers, anything that is not an address', () => {
    for (const bad of [
      'javascript:alert(1)',
      'JaVaScRiPt:alert(1)',
      'file:///C:/Windows/System32/calc.exe',
      'ms-msdt:/id PCWDiagnostic /skip force /param "IT_RebrowseForFile=x"',
      'ms-settings:privacy',
      'search-ms:query=x',
      'vbscript:msgbox(1)',
      'ftp://example.com/x',
      'ws://example.com/',
      'tel:+15551234',
      'steam://run/1',
      'http://[bad',
      'https://',
    ]) {
      expect(kind(bad)).toEqual({ kind: 'refused' });
    }
  });
});

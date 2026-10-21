import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { LearnMore } from '../../ui/learn-more.tsx';
import { Page } from '../../ui/layout.tsx';
import { BookOpen } from 'lucide';
import { cleanup, mount, type, waitFor } from '../../test/render.tsx';
import { GuideScreen } from './guide-screen.tsx';
import { GUIDE_SECTIONS } from './model.ts';
import { GUIDE_TOPICS } from './topics.ts';

afterEach(cleanup);

describe('the Guide screen', () => {
  it('lists every topic by section, and opens the list of sections when no topic is asked for', () => {
    const r = mount(<GuideScreen />);
    expect(r.$('h1')!.textContent).toContain('Guide');
    const nav = r.$('nav[aria-label="Guide topics"]')!;
    for (const s of GUIDE_SECTIONS) expect(nav.textContent).toContain(s.title);
    expect(nav.querySelectorAll('a').length).toBe(GUIDE_TOPICS.length);
    expect(r.$('[data-testid="guide-topic"]')).toBeNull();
    // the body lists the same topics with their summaries
    expect(r.$('main, section')!.textContent).toContain(GUIDE_TOPICS[0]!.summary);
  });

  it('shows one topic, marks it current in the list and offers the topics to read next', () => {
    const r = mount(<GuideScreen topic="scan" />);
    const art = r.$('[data-testid="guide-topic"]')!;
    expect(art.getAttribute('data-topic')).toBe('scan');
    expect(art.querySelector('h2')!.textContent).toBe('Scan a repository for problems');
    expect(art.textContent).toContain('Drydock comes with its rules built in');
    expect(r.$('nav a[aria-current="page"]')!.getAttribute('href')).toBe('/guide/scan');
    const next = [...art.querySelectorAll('aside[aria-label="Related topics"] a')].map((a) => a.getAttribute('href'));
    expect(next).toContain('/guide/rule-licences');
    // a link in a topic to another page of the app is a plain link; one to a website opens in a new tab
    const ext = r.$('[data-testid="guide-topic"] a[href^="https://"]');
    expect(ext).toBeNull();
    const gh = mount(<GuideScreen topic="github" />);
    const site = gh.$('[data-testid="guide-topic"] a[href="https://cli.github.com"]')!;
    expect(site.getAttribute('target')).toBe('_blank');
    expect(site.getAttribute('rel')).toContain('noopener');
    expect(site.hasAttribute('data-rmx-document')).toBe(true);
  });

  it('says so when a topic does not exist, and links back to the list', () => {
    const r = mount(<GuideScreen topic="nope" />);
    expect(r.$('[data-testid="guide-topic"]')).toBeNull();
    expect(Array.from(r.$$('h2')).map((h) => h.textContent)).toContain('No such topic');
    expect(r.$('a[href="/guide"]')).not.toBeNull();
  });

  it('searches as you type and shows what matched, or says nothing was found', async () => {
    const r = mount(<GuideScreen />);
    const box = r.$('input[aria-label="Search the Guide"]') as HTMLInputElement;
    type(box, 'gitleaks');
    await waitFor(() => expect(r.$('[data-testid="guide-results"]')).not.toBeNull(), r);
    const res = r.$('[data-testid="guide-results"]')!;
    expect(res.textContent).toContain('Scan a repository for problems');
    type(box, 'zzzz-nothing');
    await waitFor(() => expect(r.$('[data-testid="guide-results"]')!.textContent).toContain('Nothing found'), r);
    type(box, '');
    await waitFor(() => expect(r.$('[data-testid="guide-results"]')).toBeNull(), r);
  });
});

describe('the Learn more link of a page', () => {
  it('sits beside the subtitle and names its topic for assistive technology', () => {
    const r = mount(
      <Page title="Routing" icon={BookOpen} subtitle="Model routing" guide="routing-rules">
        <p>x</p>
      </Page>,
    );
    const a = r.$('a[data-testid="learn-more"]')!;
    expect(a.getAttribute('href')).toBe('/guide/routing-rules');
    expect(a.getAttribute('aria-label')).toBe('Learn more: Model routing and its rules');
    expect(r.$('h1')!.parentElement!.textContent).toContain('Model routing');
  });

  it('can stand alone in a section', () => {
    const r = mount(<LearnMore topic="budgets" label="How budgets work" />);
    expect(r.$('a')!.textContent).toContain('How budgets work');
    expect(r.$('a')!.getAttribute('href')).toBe('/guide/budgets');
  });
});

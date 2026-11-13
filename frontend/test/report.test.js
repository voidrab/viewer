import { describe, expect, it } from 'vitest';

import { MAX_PAGE_LINKS, renderConversion, renderIssues, renderPages, renderReport } from '../src/report.js';

const issue = (overrides = {}) => ({
  clause: '6.3.4/1',
  description: 'Fonts must be embedded',
  count: 3,
  pages: [0, 1, 2],
  messages: ['font Courier is not embedded'],
  ...overrides,
});

const report = (overrides = {}) => ({ valid: false, total: 3, issues: [issue()], millis: 12, ...overrides });

describe('renderPages', () => {
  it('lists the document and each page as a link', () => {
    const node = renderPages([0, 2, 5]);
    expect(node.textContent).toBe('Where: document, p. 2, p. 5');
    expect([...node.querySelectorAll('[data-page]')].map((b) => b.dataset.page)).toEqual(['2', '5']);
  });

  it('collapses pages beyond the limit', () => {
    const pages = Array.from({ length: MAX_PAGE_LINKS + 3 }, (_, i) => i + 1);
    const node = renderPages(pages);
    expect(node.querySelectorAll('[data-page]')).toHaveLength(MAX_PAGE_LINKS);
    expect(node.textContent).toContain('and 3 more pages');
  });

  it('handles document-only issues', () => {
    expect(renderPages([0]).textContent).toBe('Where: document');
  });
});

describe('renderIssues', () => {
  it('renders one expandable entry per issue', () => {
    const node = renderIssues([issue(), issue({ clause: '6.7.2/1', count: 1, messages: ['a', 'b'] })]);
    const items = node.querySelectorAll('.issues > li');
    expect(items).toHaveLength(2);
    expect(items[0].querySelector('summary').textContent).toBe('6.3.4/1 Fonts must be embedded×3');
    expect(items[1].querySelectorAll('.messages li')).toHaveLength(2);
  });
});

describe('renderReport', () => {
  it('reports a pass', () => {
    const node = renderReport(report({ valid: true, total: 0, issues: [] }), 'PDF/A-1b');
    expect(node.querySelector('.verdict.ok').textContent).toBe('✓ Conforms to PDF/A-1b');
    expect(node.querySelector('.meta').textContent).toBe('PDF/A-1b · checked in 12 ms');
    expect(node.querySelectorAll('.issues li')).toHaveLength(0);
  });

  it('reports failures with counts', () => {
    const node = renderReport(report({ total: 1 }), 'PDF/A-1b');
    expect(node.querySelector('.verdict.bad').textContent).toBe('✗ 1 issue in 1 check');
    expect(node.querySelectorAll('.issues > li')).toHaveLength(1);
  });
});

describe('renderConversion', () => {
  const cr = (overrides = {}) => ({
    output: '/docs/report.pdfa.pdf',
    iterations: 2,
    report: report({ valid: true, total: 0, issues: [] }),
    rasterized: [],
    lost: [],
    ...overrides,
  });

  it('reports a clean conversion with an open button', () => {
    const node = renderConversion(cr(), 'PDF/A-2b');
    expect(node.querySelector('.verdict.ok').textContent).toBe('✓ Converted to PDF/A-2b');
    expect(node.querySelector('.meta').textContent).toBe('Saved as report.pdfa.pdf · 2 passes · 12 ms');
    expect(node.querySelector('[data-open]').dataset.open).toBe('/docs/report.pdfa.pdf');
    expect(node.querySelector('.warn')).toBeNull();
  });

  it('warns about residual issues, rasterized pages and losses', () => {
    const node = renderConversion(
      cr({ iterations: 1, report: report({ total: 2 }), rasterized: [3, 4], lost: ['page 3: not drawn: shading'] }),
      'PDF/A-1b',
    );
    expect(node.querySelector('.verdict.bad').textContent).toBe('✗ Converted, but 2 issues remain');
    expect(node.querySelector('.meta').textContent).toContain('1 pass ·');
    const warnings = [...node.querySelectorAll('.warn')].map((w) => w.textContent);
    expect(warnings[0]).toContain('pages 3, 4');
    expect(warnings[1]).toBe('Not carried over:');
    expect(node.querySelector('.messages li').textContent).toBe('page 3: not drawn: shading');
    expect(node.querySelectorAll('.issues > li')).toHaveLength(1);
  });
});

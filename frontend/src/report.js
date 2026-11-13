// Renders verification and conversion reports as DOM nodes. Text from the
// PDF (messages, file names) is only ever set as text, never parsed as HTML.

import { el } from './dom.js';
import { baseName, plural } from './format.js';

/** Max page links shown per issue before collapsing the rest into a count. */
export const MAX_PAGE_LINKS = 12;

/**
 * Lists the pages an issue occurs on as buttons carrying data-page, which
 * the app turns into navigation. Page 0 is the document as a whole.
 */
export function renderPages(pages) {
  const wrap = el('p', { className: 'pages' }, 'Where: ');
  const items = [];
  if (pages.includes(0)) items.push(el('span', {}, 'document'));
  const numbered = pages.filter((p) => p > 0);
  for (const p of numbered.slice(0, MAX_PAGE_LINKS)) {
    items.push(el('button', { type: 'button', className: 'link', 'data-page': p }, `p. ${p}`));
  }
  if (numbered.length > MAX_PAGE_LINKS) {
    items.push(el('span', {}, `and ${numbered.length - MAX_PAGE_LINKS} more pages`));
  }
  items.forEach((item, i) => wrap.append(...(i ? [', ', item] : [item])));
  return wrap;
}

function renderIssue(issue) {
  const summary = el(
    'summary',
    {},
    el('code', {}, issue.clause),
    ' ',
    el('span', { className: 'desc' }, issue.description),
    el('span', { className: 'count' }, `×${issue.count}`),
  );
  const messages = el('ul', { className: 'messages' }, ...issue.messages.map((m) => el('li', {}, m)));
  return el('li', {}, el('details', {}, summary, renderPages(issue.pages), messages));
}

/** Renders a report's issues as a list, empty when there are none. */
export function renderIssues(issues) {
  return el('ul', { className: 'issues' }, ...issues.map(renderIssue));
}

function verdict(ok, text) {
  return el('p', { className: `verdict ${ok ? 'ok' : 'bad'}` }, ok ? '✓ ' : '✗ ', text);
}

/**
 * Renders a verification report.
 * @param {object} report the Go pdfa.Report
 * @param {string} label the profile's display label, e.g. "PDF/A-1b"
 */
export function renderReport(report, label) {
  const head = report.valid
    ? verdict(true, `Conforms to ${label}`)
    : verdict(false, `${plural(report.total, 'issue')} in ${plural(report.issues.length, 'check')}`);
  return el(
    'div',
    { className: 'report' },
    head,
    el('p', { className: 'meta' }, `${label} · checked in ${report.millis} ms`),
    renderIssues(report.issues),
  );
}

/**
 * Renders a conversion result, with a button carrying data-open to open the
 * output.
 * @param {object} cr the Go pdfa.ConvertReport
 * @param {string} label the profile's display label
 */
export function renderConversion(cr, label) {
  const { report } = cr;
  const parts = [
    report.valid
      ? verdict(true, `Converted to ${label}`)
      : verdict(false, `Converted, but ${plural(report.total, 'issue')} remain`),
    el('p', { className: 'meta' }, `Saved as ${baseName(cr.output)} · ${plural(cr.iterations, 'pass', 'passes')} · ${report.millis} ms`),
    el('button', { type: 'button', className: 'primary', 'data-open': cr.output }, 'Open converted file'),
  ];
  if (cr.rasterized.length) {
    parts.push(
      el(
        'p',
        { className: 'warn' },
        `Rebuilt as images (text no longer selectable): pages ${cr.rasterized.join(', ')}`,
      ),
    );
  }
  if (cr.lost.length) {
    parts.push(
      el('p', { className: 'warn' }, 'Not carried over:'),
      el('ul', { className: 'messages' }, ...cr.lost.map((l) => el('li', {}, l))),
    );
  }
  parts.push(renderIssues(report.issues));
  return el('div', { className: 'report' }, ...parts);
}

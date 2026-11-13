import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, it } from 'vitest';

// Regression guard: pdf.js sizes each page as a content box with a border and
// positions its canvas and text layer against that. A box-sizing rule that
// reaches the document area shrinks the canvas but not the text layer, so
// selection and find highlights drift off the rendered text. Every box-sizing
// rule must therefore exclude #container and everything inside it.
it('keeps box-sizing rules out of the document area', () => {
  const css = readFileSync(resolve(import.meta.dirname, '../src/style.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
  const selectors = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter(([, , body]) => /box-sizing/.test(body))
    .flatMap(([, selector]) => selector.split(/,(?![^(]*\))/).map((s) => s.trim()));
  expect(selectors.length).toBeGreaterThan(0);
  expect(selectors.filter((s) => !s.includes(':not(#container, #container *)'))).toEqual([]);
});

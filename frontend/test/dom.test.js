import { describe, expect, it } from 'vitest';

import { el } from '../src/dom.js';

describe('el', () => {
  it('sets properties, data attributes and children', () => {
    const node = el('button', { className: 'x', type: 'button', 'data-page': 4 }, 'Go ', el('b', {}, 'now'));
    expect(node.tagName).toBe('BUTTON');
    expect(node.className).toBe('x');
    expect(node.dataset.page).toBe('4');
    expect(node.textContent).toBe('Go now');
  });

  it('sets dashed and unknown names as attributes', () => {
    const node = el('div', { role: 'tab', 'aria-selected': 'true', title: 'x' });
    expect(node.getAttribute('role')).toBe('tab');
    expect(node.getAttribute('aria-selected')).toBe('true');
    expect(node.title).toBe('x');
  });

  it('never parses text as HTML', () => {
    const node = el('p', {}, '<img src=x onerror=alert(1)>');
    expect(node.querySelector('img')).toBeNull();
    expect(node.textContent).toContain('<img');
  });
});

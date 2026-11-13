// The one DOM helper: builds an element from properties and children. Text
// children are set as text, never parsed as HTML.

/**
 * Creates an element: el('p', {className: 'x'}, 'text', childNode). Keys
 * that are element properties are set as such; dashed names (data-*, aria-*)
 * and anything else become attributes.
 */
export function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (!key.includes('-') && key in node) node[key] = value;
    else node.setAttribute(key, value);
  }
  node.append(...children);
  return node;
}

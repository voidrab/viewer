// The PDF engine: pdf.js, its viewer components and their stylesheet. main.js
// imports this lazily, when a window first opens a document, so empty windows
// and tabs never load pdf.js at all.

import 'pdfjs-dist/web/pdf_viewer.css';

import { pdfjs } from './pdfjs.js'; // must precede the viewer components
import * as viewerLib from 'pdfjs-dist/web/pdf_viewer.mjs';

import { createDocumentView } from './document.js';

/** Creates the window's document view in container. */
export function createView(container, assetBase) {
  return createDocumentView({ container, pdfjs, viewerLib, assetBase });
}

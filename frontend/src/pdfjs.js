// Configures pdf.js and publishes it on globalThis, where pdf.js's viewer
// components look for it. main.js imports this module before the viewer
// components, and ES modules evaluate in import order.

import * as pdfjs from 'pdfjs-dist';
import workerSrc from 'pdfjs-dist/build/pdf.worker.min.mjs?url';

pdfjs.GlobalWorkerOptions.workerSrc = workerSrc;
globalThis.pdfjsLib = pdfjs;

export { pdfjs };

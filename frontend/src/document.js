// Wraps pdf.js's PDFViewer: lazy page rendering, text selection, links and
// find come from pdf.js itself; this module only gives the app a small API.

/** Zoom presets offered in the toolbar, in menu order. */
export const ZOOM_PRESETS = [
  ['page-width', 'fit width'],
  ['page-fit', 'fit page'],
  ['0.5', '50%'],
  ['0.75', '75%'],
  ['1', '100%'],
  ['1.25', '125%'],
  ['1.5', '150%'],
  ['2', '200%'],
  ['3', '300%'],
];

/**
 * Returns the URL prefix of pdf.js's data directories (CMaps, fonts,
 * decoders): copied next to the bundle by vite.config.js in a build, served
 * straight from node_modules by the dev server.
 */
export function assetBaseFor(env) {
  return env.DEV ? '/node_modules/pdfjs-dist/' : './pdfjs/';
}

/**
 * Caps a page canvas at 8.4 megapixels (pdf.js defaults to 33.5): about 34
 * MB per canvas instead of 134. Pages zoomed past it render a little softer.
 */
export const MAX_CANVAS_PIXELS = 2 ** 23;

/**
 * One pdf.js viewer that shows one document at a time. Documents are opened
 * once and can then be shown, hidden (when their tab goes to the background)
 * and closed; a hidden document keeps only its parsed structure, not its
 * rendered pages.
 *
 * @param {object} deps
 * @param {HTMLElement} deps.container the scrolling element; it must be
 *   absolutely positioned and contain a div.pdfViewer
 * @param {object} deps.pdfjs the pdfjs-dist module
 * @param {object} deps.viewerLib the pdfjs-dist/web/pdf_viewer module
 * @param {string} deps.assetBase URL prefix of the copied pdf.js data dirs
 */
export function createDocumentView({ container, pdfjs, viewerLib, assetBase }) {
  const { EventBus, PDFLinkService, PDFFindController, PDFViewer, LinkTarget } = viewerLib;
  const eventBus = new EventBus();
  const linkService = new PDFLinkService({ eventBus, externalLinkTarget: LinkTarget.BLANK });
  const findController = new PDFFindController({ eventBus, linkService });
  const viewer = new PDFViewer({
    container,
    eventBus,
    linkService,
    findController,
    maxCanvasPixels: MAX_CANVAS_PIXELS,
  });
  linkService.setViewer(viewer);

  // The standalone PDFViewer does not refit when its container changes size
  // (window resize, maximise, side panel), so keep a fit preset fitted.
  let frame = 0;
  new ResizeObserver(() => {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      const value = viewer.currentScaleValue;
      if (viewer.pagesCount > 0 && Number.isNaN(Number(value))) viewer.currentScaleValue = value;
    });
  }).observe(container);

  let shown = null; // the handle in the viewer
  let restore = null; // the state to apply once its pages are laid out

  eventBus.on('pagesinit', () => {
    if (!restore) return;
    viewer.currentScaleValue = restore.scale;
    container.scrollTop = restore.top;
    container.scrollLeft = restore.left;
    restore = null;
  });

  return {
    /**
     * Opens the PDF at url without showing it. Only the byte ranges pdf.js
     * needs are fetched, so a large file is never held in memory whole.
     * @returns {Promise<{pages: number}>} a handle for show and close
     */
    async open(url) {
      const task = pdfjs.getDocument({
        url,
        disableAutoFetch: true,
        disableStream: true,
        cMapUrl: `${assetBase}cmaps/`,
        cMapPacked: true,
        standardFontDataUrl: `${assetBase}standard_fonts/`,
        wasmUrl: `${assetBase}wasm/`,
        isEvalSupported: false,
        enableScripting: false,
      });
      try {
        const pdf = await task.promise;
        return { task, pdf, pages: pdf.numPages };
      } catch (err) {
        task.destroy();
        throw err;
      }
    },
    /**
     * Shows an opened document at a zoom (a preset name or a scale) and
     * scroll position.
     * @param {{scale: string, top: number, left: number}} state
     */
    show(handle, state) {
      restore = state;
      shown = handle;
      viewer.setDocument(handle.pdf);
      linkService.setDocument(handle.pdf, null);
    },
    /**
     * Takes the shown document out of the viewer, dropping its rendered pages
     * and pdf.js's caches for it.
     * @returns {{scale: string, top: number, left: number}} where it was, for show
     */
    hide() {
      const state = { scale: viewer.currentScaleValue, top: container.scrollTop, left: container.scrollLeft };
      viewer.setDocument(null);
      linkService.setDocument(null, null);
      shown.pdf.cleanup();
      shown = null;
      return state;
    },
    /** Closes an opened document for good, hiding it first if shown. */
    close(handle) {
      if (handle === shown) this.hide();
      handle.task.destroy();
    },
    get page() {
      return viewer.currentPageNumber;
    },
    get pageCount() {
      return viewer.pagesCount;
    },
    goToPage(n) {
      viewer.currentPageNumber = n;
    },
    next: () => viewer.nextPage(),
    previous: () => viewer.previousPage(),
    zoomIn: () => viewer.increaseScale(),
    zoomOut: () => viewer.decreaseScale(),
    setZoom(value) {
      viewer.currentScaleValue = value;
    },
    /**
     * Searches the document; matches are highlighted by pdf.js.
     * @param {string} query
     * @param {{again?: boolean, previous?: boolean}} [opts] again moves to
     *   the next (or previous) match of the same query
     */
    find(query, { again = false, previous = false } = {}) {
      eventBus.dispatch('find', {
        source: null,
        type: again ? 'again' : '',
        query,
        caseSensitive: false,
        entireWord: false,
        highlightAll: true,
        findPrevious: previous,
        matchDiacritics: false,
      });
    },
    /** Subscribes to a pdf.js viewer event (pagechanging, scalechanging, ...). */
    on(name, cb) {
      eventBus.on(name, cb);
    },
  };
}

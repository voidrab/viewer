import { cpSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { defineConfig } from 'vite';

const pdfjsDir = resolve(import.meta.dirname, 'node_modules/pdfjs-dist');

// Copies the data pdf.js loads at runtime next to the bundle: CMaps for CJK
// text, the standard 14 fonts for PDFs that do not embed them, and the
// JPEG 2000 / JBIG2 / colour-management decoders. Left out: the JavaScript
// sandbox (quickjs), as the viewer does not run PDF scripts, and the decoders'
// no-WebAssembly fallbacks, as every Wails webview runs WebAssembly.
function pdfjsAssets() {
  return {
    name: 'pdfjs-assets',
    apply: 'build',
    writeBundle({ dir }) {
      for (const sub of ['cmaps', 'standard_fonts', 'wasm']) {
        cpSync(resolve(pdfjsDir, sub), resolve(dir, 'pdfjs', sub), {
          recursive: true,
          filter: (src) => !/quickjs|nowasm/i.test(src),
        });
      }
      // Go embeds frontend/dist, which must exist before the first build.
      writeFileSync(resolve(dir, '.gitkeep'), '');
    },
  };
}

export default defineConfig({
  base: './',
  plugins: [pdfjsAssets()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    chunkSizeWarningLimit: 2048,
  },
  test: {
    environment: 'happy-dom',
    include: ['test/**/*.test.js'],
    restoreMocks: true,
    coverage: {
      provider: 'v8',
      include: ['src/**/*.js'],
      reporter: ['text', 'html'],
      thresholds: { lines: 100, functions: 100, branches: 100, statements: 100 },
    },
  },
});

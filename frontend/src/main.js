// Entry point: starts the app with the Wails runtime. pdf.js loads later, on
// the first document (see engine.js).

import './style.css';

import { Browser, Call, Events, System, Window } from '@wailsio/runtime';

import { createApi } from './api.js';
import { createApp } from './app.js';
import { assetBaseFor } from './document.js';

export const app = createApp({
  document,
  api: createApi({ Call, Events, Browser, System, Window }),
  loadView: async () => {
    const { createView } = await import('./engine.js');
    return createView(document.getElementById('container'), assetBaseFor(import.meta.env));
  },
});

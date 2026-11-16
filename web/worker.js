// The web worker Viewrab's WebAssembly program runs in, so that rendering
// never holds up the page. viewrab.js starts it and hands it the page's
// canvas as an OffscreenCanvas; from then on the page forwards the canvas's
// events here, and this worker asks the page, through toPage, for what only
// the page can do: the file chooser, the clipboard, downloads, the title, the
// cursor and the local storage.
"use strict";

importScripts("memfs.js", "wasm_exec.js");

(() => {
	// Messages from the page wait here until the program listens.
	let listener = null;
	const early = [];
	const deliver = (msg) => (listener ? listener(msg) : early.push(msg));

	// Requests to the page that expect an answer, by id.
	let nextCall = 1;
	const calls = new Map();
	const ask = (op, arg, transfer = []) => new Promise((resolve, reject) => {
		const id = nextCall++;
		calls.set(id, { resolve, reject });
		postMessage({ op, arg, id }, transfer);
	});

	const importFiles = async (files) => {
		const paths = [];
		for (const f of Array.from(files)) {
			paths.push(memfs.put(f.name, new Uint8Array(await f.arrayBuffer())));
		}
		return paths;
	};

	let darkWatch = null;

	// requestAnimationFrame runs in dedicated workers in current browsers; a
	// timer at about the display's rate stands in where it does not.
	const requestFrame = globalThis.requestAnimationFrame
		? (f) => requestAnimationFrame(f)
		: (f) => setTimeout(f, 16);

	const host = {
		canvas: null,
		params: {},
		storage: {},
		prefersDark: false,
		listen(f) {
			listener = f ? (msg) => f(msg) : null;
			while (listener && early.length) listener(early.shift());
		},
		toPage(op, arg) { postMessage({ op, arg }); },
		requestFrame,
		importFiles,
		pickFiles: async () => importFiles(await ask("pickFiles")),
		fetchFile: async (url) => {
			const res = await fetch(url);
			if (!res.ok) throw new Error(res.status + " " + res.statusText);
			const name = decodeURIComponent(new URL(res.url).pathname.split("/").pop()) || "document.pdf";
			return memfs.put(name, new Uint8Array(await res.arrayBuffer()));
		},
		download(path) {
			const bytes = memfs.get(path).slice(); // a copy the page may take
			postMessage({ op: "download", arg: { name: memfs.base(path), bytes } }, [bytes.buffer]);
		},
		store(key, value) {
			if (value === "") delete host.storage[key]; else host.storage[key] = value;
			postMessage({ op: "store", arg: { key, value } });
		},
		onDark(f) { darkWatch = f; },
		started: () => postMessage({ op: "status", arg: { text: "" } }),
		exited: () => postMessage({ op: "status", arg: { text: "Viewrab has closed. Reload the page to start it again." } }),
		failed: (msg) => postMessage({ op: "status", arg: { text: "Viewrab could not start: " + msg, failed: true } }),
	};
	globalThis.viewrab = host;

	const start = async (init) => {
		Object.assign(host, {
			canvas: init.canvas, params: init.params, storage: init.storage, prefersDark: init.prefersDark,
		});
		deliver({ type: "resize", w: init.w, h: init.h });
		const go = new Go();
		const res = fetch(init.wasm);
		const { instance } = await WebAssembly.instantiateStreaming(res, go.importObject).catch(async () =>
			WebAssembly.instantiate(await (await fetch(init.wasm)).arrayBuffer(), go.importObject));
		await go.run(instance);
	};

	onmessage = ({ data }) => {
		switch (data.type) {
		case "init":
			start(data).catch((err) => host.failed(String(err)));
			break;
		case "reply": {
			const c = calls.get(data.id);
			calls.delete(data.id);
			if (data.error) c?.reject(new Error(data.error)); else c?.resolve(data.value);
			break;
		}
		case "dark":
			darkWatch?.(data.dark);
			break;
		default:
			deliver(data);
		}
	};
})();

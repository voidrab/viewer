// The web workers Viewrab's WebAssembly program runs in, so that rendering
// never holds up the page. The same program runs in two roles.
//
// The viewer worker, which viewrab.js starts, draws the window on the page's
// canvas, an OffscreenCanvas it is handed. The page forwards the canvas's
// events to it, and it asks the page, through toPage, for what only the page
// can do: the file chooser, the clipboard, downloads, the title, the cursor
// and the local storage.
//
// Render workers, which the viewer worker starts, only render pages. Go gives
// a worker one thread, which a render would hold for as long as it takes, so
// the viewer hands every render to them: it stays free to scroll, and pages
// render on as many cores as there are render workers. Each keeps its own copy
// of the files it renders, sent when it first needs them.
"use strict";

// The build's version, which the page adds to the worker's address, for the
// files of the same build.
const version = location.search;
// asset returns the address of the build's file at path.
const asset = (path) => path + version;
importScripts(asset("memfs.js"), asset("wasm_exec.js"));

(() => {
	// Messages for the program wait here until it listens.
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

	// fetchOK fetches url, failing unless the server answers with it.
	const fetchOK = async (url) => {
		const res = await fetch(url);
		if (!res.ok) throw new Error(res.status + " " + res.statusText);
		return res;
	};

	const importFiles = async (files) => {
		files = Array.from(files);
		const buffers = await Promise.all(files.map((f) => f.arrayBuffer()));
		return files.map((f, i) => memfs.put(f.name, new Uint8Array(buffers[i])));
	};

	let darkWatch = null;

	// requestAnimationFrame runs in dedicated workers in current browsers; a
	// timer at about the display's rate stands in where it does not.
	const requestFrame = globalThis.requestAnimationFrame
		? (f) => requestAnimationFrame(f)
		: (f) => setTimeout(f, 16);

	// Rendering in render workers, from the viewer worker's side. A job goes
	// to a render worker only when it is idle, so a job dropped before then
	// costs nothing, and the next job is chosen when one finishes.
	const renderers = [];
	const queue = [];
	const jobs = new Map(); // id → job, until it is answered or dropped
	let nextJob = 1;

	const pump = () => {
		if (renderers.length && renderers.every((r) => r.dead)) {
			for (const job of queue.splice(0)) {
				jobs.delete(job.id);
				job.reject(new Error("no render worker is left"));
			}
			return;
		}
		for (const r of renderers) {
			if (r.busy || r.dead) continue;
			const job = queue.shift();
			if (!job) return;
			let key;
			try {
				key = memfs.version(job.path);
			} catch (err) {
				jobs.delete(job.id);
				job.reject(err);
				continue;
			}
			r.busy = job;
			if (r.files.get(job.path) !== key) {
				const bytes = memfs.get(job.path).slice(); // a copy the render worker takes
				r.worker.postMessage({ type: "file", path: job.path, bytes }, [bytes.buffer]);
				r.files.set(job.path, key);
			}
			r.worker.postMessage({ type: "job", id: job.id, path: job.path, key, page: job.page, scale: job.scale,
				x0: job.rect[0], y0: job.rect[1], x1: job.rect[2], y1: job.rect[3] });
		}
	};

	const finished = (r, { id, image, error, dropped }) => {
		for (const p of dropped || []) r.files.delete(p);
		r.busy = null;
		const job = jobs.get(id);
		jobs.delete(id);
		if (job) {
			if (error) job.reject(new Error(error)); else job.resolve(image);
		}
		pump();
	};

	const startRenderers = (module, n) => {
		for (let i = 0; i < n; i++) {
			let worker;
			try {
				worker = new Worker(asset("worker.js"));
			} catch {
				break; // no workers in workers: the viewer renders itself
			}
			const r = { worker, busy: null, dead: false, files: new Map() };
			// A render worker whose program stopped, as a panic stops it, is
			// left out from then on, and another takes its job.
			const lost = () => {
				r.dead = true;
				const job = r.busy;
				r.busy = null;
				if (job && jobs.has(job.id)) queue.unshift(job);
				pump();
			};
			worker.onmessage = ({ data }) => {
				if (data.op === "finish") finished(r, data.arg);
				else if (data.op === "exit") lost();
			};
			worker.onerror = lost;
			worker.postMessage({ type: "init", role: "renderer", module });
			renderers.push(r);
		}
		return renderers.length;
	};

	const host = {
		role: "viewer",
		canvas: null,
		params: {},
		storage: {},
		prefersDark: false,
		renderers: 0,
		listen(f) {
			listener = f || null;
			while (listener && early.length) listener(early.shift());
		},
		toPage(op, arg) { postMessage({ op, arg }); },
		requestFrame,
		importFiles,
		pickFiles: async () => importFiles(await ask("pickFiles")),
		fetchFile: async (url) => {
			const res = await fetchOK(url);
			const name = decodeURIComponent(new URL(res.url).pathname.split("/").pop()) || "document.pdf";
			return memfs.put(name, new Uint8Array(await res.arrayBuffer()));
		},
		// fetchFont fetches a substitute font for gopdfrab, which the build
		// leaves out of the program, from the fonts directory.
		fetchFont: async (name) => {
			const res = await fetchOK(asset("fonts/" + encodeURIComponent(name)));
			return new Uint8Array(await res.arrayBuffer());
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
		renderPage(path, page, scale, x0, y0, x1, y1) {
			const id = nextJob++;
			let job;
			const promise = new Promise((resolve, reject) => {
				job = { id, path, page, scale, rect: [x0, y0, x1, y1], resolve, reject };
			});
			jobs.set(id, job);
			queue.push(job);
			pump();
			return { id, promise };
		},
		cancelRender(id) {
			const job = jobs.get(id);
			if (!job) return;
			jobs.delete(id); // a running job's result is dropped when it comes
			const i = queue.indexOf(job);
			if (i >= 0) queue.splice(i, 1);
			job.reject(new Error("render dropped"));
		},
		// A render worker's side.
		onJob: (f) => host.listen(f),
		finish(result) {
			const transfer = result.image ? [result.image.pix.buffer] : [];
			postMessage({ op: "finish", arg: result }, transfer);
		},
		forget(path) { memfs.remove(path); },
		started: () => postMessage({ op: "status", arg: { text: "" } }),
		exited: () => postMessage({ op: "status", arg: { text: "Viewrab has closed. Reload the page to start it again." } }),
		failed: (msg) => postMessage({ op: "status", arg: { text: "Viewrab could not start: " + msg, failed: true } }),
	};
	globalThis.viewrab = host;

	// renderWorkers is how many render workers the viewer starts: one core is
	// left to the viewer and the page, and each worker costs memory, as it
	// runs its own copy of the program with its own copies of the files. The
	// address may say how many with ?renderers=, 0 for none, in which case
	// the viewer renders pages itself.
	const renderWorkers = (params) => {
		const asked = parseInt(params.renderers?.[0], 10);
		if (asked >= 0) return Math.min(asked, 16);
		return Math.max(1, Math.min(4, (navigator.hardwareConcurrency || 2) - 1));
	};

	const run = async (module) => {
		const go = new Go();
		const instance = await WebAssembly.instantiate(module, go.importObject);
		await go.run(instance);
	};

	// The module the page compiles while the worker starts.
	const fromPage = Promise.withResolvers();

	const startViewer = async (init) => {
		Object.assign(host, {
			canvas: init.canvas, params: init.params, storage: init.storage, prefersDark: init.prefersDark,
		});
		deliver({ type: "resize", w: init.w, h: init.h, scale: init.scale });
		const got = await fromPage.promise;
		if (got.error) throw new Error(got.error);
		const module = got.module;
		host.renderers = startRenderers(module, renderWorkers(init.params));
		await run(module);
	};

	onmessage = ({ data }) => {
		switch (data.type) {
		case "init":
			if (data.role === "renderer") {
				host.role = "renderer";
				run(data.module)
					.catch((err) => console.error("viewrab render worker:", err))
					.finally(() => postMessage({ op: "exit" }));
			} else {
				startViewer(data).catch((err) => host.failed(String(err)));
			}
			break;
		case "file": // a render worker's copy of a file
			memfs.write(data.path, data.bytes);
			break;
		case "reply": {
			const c = calls.get(data.id);
			calls.delete(data.id);
			if (data.error) c?.reject(new Error(data.error)); else c?.resolve(data.value);
			break;
		}
		case "module": // the program, compiled by the page
			fromPage.resolve(data);
			break;
		case "dark":
			darkWatch?.(data.dark);
			break;
		default:
			deliver(data);
		}
	};
})();

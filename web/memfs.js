// The in-memory file system Viewrab's WebAssembly program reads and writes
// through Go's os package: Node's fs API, which Go's syscall package calls
// under js/wasm, kept in this worker's memory. worker.js loads it before
// wasm_exec.js, so that Go finds it rather than its stub, which has no files.
"use strict";

(() => {
	// Node's open flags, which Go's syscall package reads from fs.constants.
	const O = {
		O_RDONLY: 0, O_WRONLY: 1, O_RDWR: 2, O_CREAT: 0o100, O_EXCL: 0o200,
		O_TRUNC: 0o1000, O_APPEND: 0o2000, O_DIRECTORY: 0o200000,
	};
	const S_IFREG = 0o100000, S_IFDIR = 0o040000;

	const fsError = (code) => {
		const err = new Error(code);
		err.code = code;
		return err;
	};

	// The file system: paths name nodes, a file's bytes are kept in a
	// Uint8Array that grows by doubling, and descriptors keep a position.
	const nodes = new Map();
	const fds = new Map();
	let nextFd = 3;
	let nextIno = 1;

	const now = () => Date.now();
	const mkNode = (dir, mode) => ({
		dir, mode, data: new Uint8Array(0), size: 0, ino: nextIno++, mtime: now(),
	});
	nodes.set("/", mkNode(true, 0o755));

	const normalize = (path) => {
		const parts = [];
		for (const p of String(path).split("/")) {
			if (p === "" || p === ".") continue;
			if (p === "..") parts.pop(); else parts.push(p);
		}
		return "/" + parts.join("/");
	};
	const parentOf = (path) => path.slice(0, path.lastIndexOf("/")) || "/";
	const lookup = (path) => {
		const n = nodes.get(normalize(path));
		if (!n) throw fsError("ENOENT");
		return n;
	};
	const parentDir = (path) => {
		const p = nodes.get(parentOf(path));
		if (!p) throw fsError("ENOENT");
		if (!p.dir) throw fsError("ENOTDIR");
		return p;
	};
	const fdOf = (fd) => {
		const f = fds.get(fd);
		if (!f) throw fsError("EBADF");
		return f;
	};
	const reserve = (n, size) => {
		if (size <= n.data.length) return;
		const grown = new Uint8Array(Math.max(size, n.data.length * 2, 4096));
		grown.set(n.data.subarray(0, n.size));
		n.data = grown;
	};
	const resize = (n, size) => {
		reserve(n, size);
		if (size < n.size) n.data.fill(0, size, n.size);
		n.size = size;
		n.mtime = now();
	};
	const stat = (n) => ({
		dev: 1, ino: n.ino, mode: (n.dir ? S_IFDIR : S_IFREG) | n.mode, nlink: 1,
		uid: 0, gid: 0, rdev: 0, size: n.dir ? 0 : n.size, blksize: 4096,
		blocks: Math.ceil(n.size / 512), atimeMs: n.mtime, mtimeMs: n.mtime, ctimeMs: n.mtime,
		isDirectory: () => n.dir,
	});
	const children = (path) => {
		const prefix = path === "/" ? "/" : path + "/";
		const out = [];
		for (const p of nodes.keys()) {
			if (p !== path && p.startsWith(prefix) && !p.slice(prefix.length).includes("/")) {
				out.push(p.slice(prefix.length));
			}
		}
		return out;
	};

	// Every call answers through its callback, at once; Go waits for it.
	const call = (callback, f) => {
		let result;
		try {
			result = f();
		} catch (err) {
			if (!err.code) throw err;
			callback(err);
			return;
		}
		callback(null, result);
	};

	const decoder = new TextDecoder("utf-8");
	const lines = { 1: "", 2: "" };
	const console_ = (fd, bytes) => {
		lines[fd] += decoder.decode(bytes);
		const nl = lines[fd].lastIndexOf("\n");
		if (nl !== -1) {
			(fd === 2 ? console.error : console.log)(lines[fd].substring(0, nl));
			lines[fd] = lines[fd].substring(nl + 1);
		}
	};

	const fs = {
		constants: O,
		writeSync(fd, buf) {
			console_(fd, buf);
			return buf.length;
		},
		open(path, flags, mode, callback) {
			call(callback, () => {
				path = normalize(path);
				let n = nodes.get(path);
				if (n && (flags & O.O_CREAT) && (flags & O.O_EXCL)) throw fsError("EEXIST");
				if (!n) {
					if (!(flags & O.O_CREAT)) throw fsError("ENOENT");
					parentDir(path);
					n = mkNode(false, mode & 0o777);
					nodes.set(path, n);
				}
				if ((flags & O.O_DIRECTORY) && !n.dir) throw fsError("ENOTDIR");
				const writes = (flags & 3) !== O.O_RDONLY;
				if (n.dir && writes) throw fsError("EISDIR");
				if ((flags & O.O_TRUNC) && writes) resize(n, 0);
				const fd = nextFd++;
				fds.set(fd, { path, node: n, flags, pos: 0 });
				return fd;
			});
		},
		close(fd, callback) {
			call(callback, () => { fdOf(fd); fds.delete(fd); });
		},
		read(fd, buffer, offset, length, position, callback) {
			call(callback, () => {
				const f = fdOf(fd);
				if (f.node.dir) throw fsError("EISDIR");
				const at = position === null ? f.pos : position;
				const n = Math.max(0, Math.min(length, f.node.size - at));
				buffer.set(f.node.data.subarray(at, at + n), offset);
				if (position === null) f.pos += n;
				return n;
			});
		},
		write(fd, buffer, offset, length, position, callback) {
			call(callback, () => {
				const bytes = buffer.subarray(offset, offset + length);
				if (fd === 1 || fd === 2) {
					console_(fd, bytes);
					return length;
				}
				const f = fdOf(fd);
				if ((f.flags & 3) === O.O_RDONLY) throw fsError("EBADF");
				let at = position === null ? f.pos : position;
				if (f.flags & O.O_APPEND) at = f.node.size;
				reserve(f.node, at + length);
				f.node.data.set(bytes, at);
				f.node.size = Math.max(f.node.size, at + length);
				f.node.mtime = now();
				if (position === null) f.pos = at + length;
				return length;
			});
		},
		fstat(fd, callback) { call(callback, () => stat(fdOf(fd).node)); },
		stat(path, callback) { call(callback, () => stat(lookup(path))); },
		lstat(path, callback) { call(callback, () => stat(lookup(path))); },
		mkdir(path, perm, callback) {
			call(callback, () => {
				path = normalize(path);
				if (nodes.has(path)) throw fsError("EEXIST");
				parentDir(path);
				nodes.set(path, mkNode(true, perm & 0o777));
			});
		},
		readdir(path, callback) {
			call(callback, () => {
				if (!lookup(path).dir) throw fsError("ENOTDIR");
				return children(normalize(path));
			});
		},
		unlink(path, callback) {
			call(callback, () => {
				if (lookup(path).dir) throw fsError("EISDIR");
				nodes.delete(normalize(path));
			});
		},
		rmdir(path, callback) {
			call(callback, () => {
				path = normalize(path);
				if (!lookup(path).dir) throw fsError("ENOTDIR");
				if (children(path).length) throw fsError("ENOTEMPTY");
				nodes.delete(path);
			});
		},
		rename(from, to, callback) {
			call(callback, () => {
				from = normalize(from);
				to = normalize(to);
				const n = lookup(from);
				parentDir(to);
				if (n.dir) throw fsError("EPERM"); // the viewer renames only files
				const old = nodes.get(to);
				if (old && old.dir) throw fsError("EISDIR");
				nodes.delete(from);
				nodes.set(to, n);
				for (const f of fds.values()) if (f.path === from) f.path = to;
			});
		},
		ftruncate(fd, length, callback) { call(callback, () => resize(fdOf(fd).node, length)); },
		truncate(path, length, callback) { call(callback, () => resize(lookup(path), length)); },
		fsync(fd, callback) { call(callback, () => { fdOf(fd); }); },
		chmod(path, mode, callback) { call(callback, () => { lookup(path).mode = mode & 0o777; }); },
		fchmod(fd, mode, callback) { call(callback, () => { fdOf(fd).node.mode = mode & 0o777; }); },
		chown(path, uid, gid, callback) { call(callback, () => { lookup(path); }); },
		fchown(fd, uid, gid, callback) { call(callback, () => { fdOf(fd); }); },
		lchown(path, uid, gid, callback) { call(callback, () => { lookup(path); }); },
		utimes(path, atime, mtime, callback) {
			call(callback, () => { lookup(path).mtime = mtime * 1000; });
		},
		link(path, link, callback) { callback(fsError("ENOSYS")); },
		symlink(path, link, callback) { callback(fsError("ENOSYS")); },
		readlink(path, callback) { callback(fsError("EINVAL")); },
	};

	// Directories the program expects: the temporary one, and where files
	// from the browser go, one directory each so that names may repeat.
	for (const dir of ["/tmp", "/files"]) nodes.set(dir, mkNode(true, 0o755));
	let nextImport = 1;

	// put stores bytes as a new file named name and returns its path.
	const put = (name, bytes) => {
		const path = "/files/" + nextImport++ + "/" + (String(name).replaceAll("/", "_") || "document.pdf");
		write(path, bytes);
		return path;
	};

	// get returns the bytes of the file at path.
	const get = (path) => {
		const n = lookup(path);
		return n.data.subarray(0, n.size);
	};

	globalThis.fs = fs;
	globalThis.process ??= {
		getuid: () => -1, getgid: () => -1, geteuid: () => -1, getegid: () => -1,
		getgroups() { throw fsError("ENOSYS"); },
		pid: -1, ppid: -1,
		umask: () => 0o022,
		cwd: () => "/",
		chdir() { throw fsError("ENOSYS"); },
	};
	globalThis.path ??= { resolve: (...segments) => normalize(segments.join("/")) };
	// write stores bytes as the file at path, with its directories, in place
	// of any file there.
	const write = (path, bytes) => {
		path = normalize(path);
		let dir = "";
		for (const part of parentOf(path).split("/").filter(Boolean)) {
			dir += "/" + part;
			if (!nodes.has(dir)) nodes.set(dir, mkNode(true, 0o755));
		}
		const n = mkNode(false, 0o644);
		n.data = bytes;
		n.size = bytes.length;
		nodes.set(path, n);
	};

	// version names the version of the file at path: a file put, written or
	// renamed there anew has another, one written in place keeps its own
	// until its modification time moves.
	const version = (path) => {
		const n = lookup(path);
		return n.ino + ":" + n.mtime;
	};

	const remove = (path) => { nodes.delete(normalize(path)); };

	globalThis.memfs = {
		fs, put, get, write, version, remove, base: (path) => normalize(path).split("/").pop(),
	};
})();

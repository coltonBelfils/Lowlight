'use strict';

const vscode = require('vscode');
const cp = require('child_process');
const fs = require('fs');
const path = require('path');
const { createLowlightTestController } = require('./test_controller');

/** @type {LowlightClient | null} */
let client = null;
/** @type {vscode.OutputChannel | null} */
let outputChannel = null;
/** @type {vscode.DiagnosticCollection | null} */
let diagnosticCollection = null;
/** @type {vscode.StatusBarItem | null} */
let statusBarItem = null;
/** @type {vscode.Terminal | null} */
let runTerminal = null;

/**
 * Resolves the `lowlight` compiler/LSP executable path.
 * Search order:
 *   1. `lowlight.serverPath` user/workspace setting
 *   2. Bundled binary inside the extension (`<ext>/bin/lowlight`)
 *   3. Workspace folder root (`<workspace>/lowlight`)
 *   4. Repository root relative to `<repo>/editors/vscode/lowlight`
 *   5. `lowlight` on system PATH
 *
 * @param {vscode.ExtensionContext} context
 * @returns {string}
 */
function resolveServerBinary(context) {
  const cfg = vscode.workspace.getConfiguration('lowlight');
  const configured = (cfg.get('serverPath') || '').trim();
  if (configured.length > 0) {
    return configured;
  }

  const exeName = process.platform === 'win32' ? 'lowlight.exe' : 'lowlight';
  const candidates = [
    path.join(context.extensionPath, 'bin', exeName),
  ];

  const folders = vscode.workspace.workspaceFolders || [];
  for (const folder of folders) {
    candidates.push(path.join(folder.uri.fsPath, exeName));
  }

  candidates.push(path.resolve(context.extensionPath, '..', '..', '..', exeName));

  for (const candidate of candidates) {
    try {
      if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) {
        return candidate;
      }
    } catch (_) {
      // Ignore inaccessible path candidates
    }
  }

  return exeName;
}

/**
 * Resolves `LOWLIGHT_ROOT` (the directory containing `std/*.ll`) so `import "std/..."`
 * works regardless of which directory the user opens in VS Code.
 *
 * @param {vscode.ExtensionContext} context
 * @returns {string}
 */
function resolveStdlibRoot(context) {
  const cfg = vscode.workspace.getConfiguration('lowlight');
  const configured = (cfg.get('stdlibPath') || '').trim();
  if (configured.length > 0) {
    if (
      path.basename(configured) === 'std' &&
      (fs.existsSync(path.join(configured, 'core', 'core.ll')) || fs.existsSync(path.join(configured, 'math.ll')))
    ) {
      return path.dirname(configured);
    }
    return configured;
  }

  const roots = [];
  const folders = vscode.workspace.workspaceFolders || [];
  for (const folder of folders) {
    roots.push(folder.uri.fsPath);
  }
  roots.push(context.extensionPath);
  roots.push(path.resolve(context.extensionPath, '..', '..', '..'));

  for (const root of roots) {
    try {
      const stdDir = path.join(root, 'std');
      if (
        fs.existsSync(stdDir) &&
        fs.statSync(stdDir).isDirectory() &&
        (fs.existsSync(path.join(stdDir, 'core', 'core.ll')) || fs.existsSync(path.join(stdDir, 'math.ll')))
      ) {
        return root;
      }
    } catch (_) {
      // Ignore inaccessible candidates
    }
  }

  return '';
}

/**
 * Collects dynamic formatting and inlay hint preferences sent via LSP
 * `initializationOptions` and `workspace/didChangeConfiguration`.
 */
function getLspSettings() {
  const cfg = vscode.workspace.getConfiguration('lowlight');
  return {
    format: {
      indent: cfg.get('format.indent', 'tabs'),
      tabWidth: cfg.get('format.tabWidth', 4),
      lineWidth: cfg.get('format.lineWidth', 100),
    },
    inlayHints: {
      types: cfg.get('inlayHints.types', true),
      parameters: cfg.get('inlayHints.parameters', true),
    },
    target: String(cfg.get('target', '') || '').trim(),
  };
}

/**
 * Zero-dependency JSON-RPC 2.0 LSP client over stdio (`lowlight lsp --stdio`).
 */
class LowlightClient {
  /**
   * @param {vscode.ExtensionContext} context
   * @param {vscode.OutputChannel} out
   * @param {vscode.DiagnosticCollection} diagnostics
   * @param {vscode.StatusBarItem} status
   */
  constructor(context, out, diagnostics, status) {
    this.context = context;
    this.out = out;
    this.diagnostics = diagnostics;
    this.status = status;

    /** @type {cp.ChildProcessWithoutNullStreams | null} */
    this.proc = null;
    this.nextId = 1;
    /** @type {Map<number, { resolve: (val: any) => void, reject: (err: Error) => void, method: string }>} */
    this.pending = new Map();
    this.rxBuf = Buffer.alloc(0);
    this.ready = false;
    this.stopping = false;
    /** @type {Promise<boolean> | null} */
    this.startPromise = null;
    /** @type {Set<string>} */
    this.syncedUris = new Set();
    /** @type {Map<string, vscode.Uri>} */
    this.fsPathToUri = new Map();
    /** @type {any} */
    this.serverCapabilities = {};
    /** @type {Map<string, any>} */
    this.docCache = new Map();
    /** @type {Map<string, Array<{ disposable: vscode.Disposable, exactPaths: Set<string>, isLowlightToml: boolean }>>} */
    this.dynamicWatchers = new Map();
    /** @type {boolean} */
    this.hasDynamicFileWatchers = false;
  }

  /**
   * Returns a per-(uri, version) client cache entry, resetting it when `doc.version` advances.
   * @param {vscode.TextDocument} doc
   */
  getDocCache(doc) {
    const uriStr = doc.uri.toString();
    let entry = this.docCache.get(uriStr);
    if (!entry || entry.version !== doc.version) {
      entry = {
        version: doc.version,
        inlayHintsPromise: null,
        codeLensesPromise: null,
        foldingRangesPromise: null,
        documentSymbolsPromise: null,
        semanticTokensPromise: null,
      };
      this.docCache.set(uriStr, entry);
    }
    return entry;
  }

  clearDocCaches() {
    this.docCache.clear();
  }

  /**
   * @returns {boolean}
   */
  isTraceEnabled() {
    return Boolean(vscode.workspace.getConfiguration('lowlight').get('trace', false));
  }

  /**
   * @param {string} msg
   */
  log(msg) {
    const ts = new Date().toISOString().slice(11, 23);
    this.out.appendLine(`[${ts}] ${msg}`);
  }

  /**
   * Starts the `lowlight lsp --stdio` child process and performs the LSP handshake.
   * @returns {Promise<boolean>}
   */
  start() {
    if (this.startPromise) {
      return this.startPromise;
    }
    this.startPromise = this._doStart();
    return this.startPromise;
  }

  /**
   * @returns {Promise<boolean>}
   */
  async _doStart() {
    this.stopping = false;
    this.ready = false;
    this.syncedUris.clear();

    const binPath = resolveServerBinary(this.context);
    const stdRoot = resolveStdlibRoot(this.context);

    if (process.platform !== 'win32' && path.isAbsolute(binPath) && fs.existsSync(binPath)) {
      try {
        fs.chmodSync(binPath, 0o755);
      } catch (_) {
        // Ignore chmod errors on read-only mounts
      }
    }

    const env = Object.assign({}, process.env);
    if (stdRoot) {
      env.LOWLIGHT_ROOT = stdRoot;
    }

    this.status.text = '$(sync~spin) Lowlight';
    this.status.tooltip = `Starting Lowlight LSP (${binPath})...`;
    this.status.show();

    this.log(`Starting Lowlight LSP: "${binPath}" lsp --stdio (LOWLIGHT_ROOT=${stdRoot || '<auto>'})`);

    let child;
    try {
      child = cp.spawn(binPath, ['lsp', '--stdio'], {
        stdio: ['pipe', 'pipe', 'pipe'],
        env,
      });
    } catch (err) {
      this._handleSpawnFailure(binPath, err);
      return false;
    }

    this.proc = child;

    child.stdout.on('data', (chunk) => {
      this._onStdoutData(chunk);
    });

    child.stderr.on('data', (chunk) => {
      const text = Buffer.isBuffer(chunk) ? chunk.toString('utf8') : String(chunk);
      for (const line of text.split(/\r?\n/)) {
        if (line.trim().length > 0) {
          this.log(`[server] ${line}`);
        }
      }
    });

    child.on('error', (err) => {
      if (!this.stopping) {
        this._handleSpawnFailure(binPath, err);
      }
    });

    child.on('exit', (code, signal) => {
      this.log(`Lowlight LSP exited (code=${code}, signal=${signal})`);
      this._rejectAllPending(new Error(`Lowlight LSP exited (code=${code}, signal=${signal})`));
      this._disposeDynamicWatchers();
      this.proc = null;
      this.ready = false;
      this.startPromise = null;
      this.syncedUris.clear();
      if (!this.stopping) {
        this.status.text = '$(warning) Lowlight';
        this.status.tooltip = 'Lowlight LSP stopped unexpectedly. Click to view logs or run "Lowlight: Restart Language Server".';
      }
    });

    const folders = vscode.workspace.workspaceFolders || [];
    const rootUri = folders.length > 0 ? folders[0].uri.toString() : '';
    const rootPath = folders.length > 0 ? folders[0].uri.fsPath : '';
    const workspaceFolders = folders.map((f) => ({
      uri: f.uri.toString(),
      name: f.name,
    }));

    try {
      const initResult = await this.sendRequest('initialize', {
        processId: process.pid,
        rootUri,
        rootPath,
        workspaceFolders,
        initializationOptions: getLspSettings(),
        capabilities: {
          workspace: {
            didChangeWatchedFiles: {
              dynamicRegistration: true,
            },
          },
          textDocument: {
            semanticTokens: {
              dynamicRegistration: false,
              requests: { full: true },
              tokenTypes: [
                'namespace', 'type', 'struct', 'enum', 'interface', 'typeParameter',
                'parameter', 'variable', 'property', 'enumMember', 'function',
                'method', 'keyword', 'comment', 'string', 'number', 'operator',
              ],
              tokenModifiers: [
                'declaration', 'definition', 'readonly', 'static',
                'documentation', 'modification', 'defaultLibrary',
              ],
              formats: ['relative'],
            },
          },
        },
      });

      this.serverCapabilities = (initResult && initResult.capabilities) || {};
      this.sendNotification('initialized', {});
      this.ready = true;

      const version = (initResult && initResult.serverInfo && initResult.serverInfo.version) || '1.0.0';
      this.status.text = '$(check) Lowlight';
      this.status.tooltip = `Lowlight Language Server v${version} running\nBinary: ${binPath}`;
      this.log(`Lowlight LSP v${version} initialized.`);

      // Synchronize all currently open `.ll` buffers immediately.
      for (const doc of vscode.workspace.textDocuments) {
        this.didOpen(doc);
      }
      return true;
    } catch (err) {
      this.log(`Failed to initialize Lowlight LSP: ${err && err.message ? err.message : String(err)}`);
      this.status.text = '$(error) Lowlight';
      this.status.tooltip = `Lowlight LSP failed to initialize: ${err && err.message ? err.message : String(err)}`;
      return false;
    }
  }

  /**
   * @param {string} binPath
   * @param {any} err
   */
  _handleSpawnFailure(binPath, err) {
    const msg = err && err.message ? err.message : String(err);
    this.log(`Unable to launch Lowlight binary "${binPath}": ${msg}`);
    this._rejectAllPending(new Error(msg));
    this.proc = null;
    this.ready = false;
    this.startPromise = null;
    this.status.text = '$(error) Lowlight';
    this.status.tooltip = `Cannot find or execute "${binPath}". Configure "lowlight.serverPath" in VS Code settings.`;
    vscode.window.showErrorMessage(
      `Lowlight LSP could not start ("${binPath}"): ${msg}. Set "lowlight.serverPath" or run editors/vscode/install.sh.`,
      'Open Settings',
      'Show Logs'
    ).then((choice) => {
      if (choice === 'Open Settings') {
        vscode.commands.executeCommand('workbench.action.openSettings', 'lowlight.serverPath');
      } else if (choice === 'Show Logs') {
        this.out.show(true);
      }
    });
  }

  /**
   * Gracefully shuts down the LSP child process.
   * @returns {Promise<void>}
   */
  async stop() {
    this.stopping = true;
    this.ready = false;
    this.startPromise = null;
    this._disposeDynamicWatchers();
    this.syncedUris.clear();
    this.diagnostics.clear();

    const proc = this.proc;
    if (!proc) {
      return;
    }

    try {
      await Promise.race([
        this.sendRequest('shutdown', null),
        new Promise((resolve) => setTimeout(resolve, 500)),
      ]);
      this.sendNotification('exit', null);
    } catch (_) {
      // Ignore shutdown errors if pipe is already closed
    }

    await new Promise((resolve) => {
      const timer = setTimeout(() => {
        try {
          proc.kill();
        } catch (_) {}
        resolve();
      }, 300);
      proc.once('exit', () => {
        clearTimeout(timer);
        resolve();
      });
    });

    this.proc = null;
  }

  /**
   * @param {Error} err
   */
  _rejectAllPending(err) {
    for (const [, entry] of this.pending) {
      entry.reject(err);
    }
    this.pending.clear();
  }

  /**
   * Parses incoming `Content-Length` framed JSON-RPC messages over raw UTF-8 byte buffers.
   * @param {Buffer | string} chunk
   */
  _onStdoutData(chunk) {
    const buf = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk, 'utf8');
    this.rxBuf = this.rxBuf.length === 0 ? buf : Buffer.concat([this.rxBuf, buf]);

    while (true) {
      const sepIdx = this.rxBuf.indexOf('\r\n\r\n');
      if (sepIdx === -1) {
        break;
      }

      const headerStr = this.rxBuf.subarray(0, sepIdx).toString('ascii');
      const match = /Content-Length:\s*(\d+)/i.exec(headerStr);
      if (!match) {
        this.log(`Malformed LSP header, skipping: ${JSON.stringify(headerStr)}`);
        this.rxBuf = this.rxBuf.subarray(sepIdx + 4);
        continue;
      }

      const contentLength = parseInt(match[1], 10);
      const bodyStart = sepIdx + 4;
      const bodyEnd = bodyStart + contentLength;
      if (this.rxBuf.length < bodyEnd) {
        // Wait for remaining payload bytes.
        break;
      }

      const jsonBytes = this.rxBuf.subarray(bodyStart, bodyEnd);
      this.rxBuf = this.rxBuf.subarray(bodyEnd);

      const jsonText = jsonBytes.toString('utf8');
      if (this.isTraceEnabled()) {
        this.log(`<- ${jsonText}`);
      }

      let msg;
      try {
        msg = JSON.parse(jsonText);
      } catch (err) {
        this.log(`Invalid JSON from server: ${err}`);
        continue;
      }

      this._dispatchMessage(msg);
    }
  }

  /**
   * @param {any} msg
   */
  _dispatchMessage(msg) {
    if (
      msg &&
      msg.id !== undefined &&
      msg.id !== null &&
      (Object.prototype.hasOwnProperty.call(msg, 'result') || Object.prototype.hasOwnProperty.call(msg, 'error'))
    ) {
      const entry = this.pending.get(msg.id);
      if (entry) {
        this.pending.delete(msg.id);
        if (msg.error) {
          entry.reject(new Error(msg.error.message || `LSP error ${msg.error.code}`));
        } else {
          entry.resolve(msg.result);
        }
      }
      return;
    }

    if (msg && typeof msg.method === 'string') {
      if (msg.method === 'textDocument/publishDiagnostics' && msg.params) {
        this._handleDiagnostics(msg.params);
      } else if (msg.method === 'window/showMessage' && msg.params) {
        const text = msg.params.message || '';
        const type = msg.params.type || 3;
        if (type === 1) {
          vscode.window.showErrorMessage(text);
        } else if (type === 2) {
          vscode.window.showWarningMessage(text);
        } else {
          vscode.window.showInformationMessage(text);
        }
      } else if (msg.method === 'window/showDocument' && msg.params) {
        this._handleShowDocument(msg.id, msg.params);
      } else if (msg.method === 'client/registerCapability' && msg.params) {
        this._handleRegisterCapability(msg.id, msg.params);
      } else if (msg.method === 'client/unregisterCapability' && msg.params) {
        this._handleUnregisterCapability(msg.id, msg.params);
      } else if (msg.id !== undefined && msg.id !== null) {
        this._writeFrame({ jsonrpc: '2.0', id: msg.id, result: null });
      }
    }
  }

  _disposeDynamicWatchers() {
    for (const entry of this.dynamicWatchers.values()) {
      if (entry && Array.isArray(entry.disposables)) {
        for (const d of entry.disposables) {
          try {
            d.dispose();
          } catch (_) {}
        }
      }
    }
    this.dynamicWatchers.clear();
    this.hasDynamicFileWatchers = false;
  }

  _disposeRegistration(regId) {
    const existing = this.dynamicWatchers.get(regId);
    if (!existing) {
      return;
    }
    if (Array.isArray(existing.disposables)) {
      for (const d of existing.disposables) {
        try {
          d.dispose();
        } catch (_) {}
      }
    }
    this.dynamicWatchers.delete(regId);
  }

  /**
   * @param {string | { baseUri?: string, pattern?: string }} globPattern
   */
  _parseWatcherGlob(globPattern) {
    if (globPattern && typeof globPattern === 'object' && typeof globPattern.baseUri === 'string' && typeof globPattern.pattern === 'string') {
      return {
        pattern: new vscode.RelativePattern(vscode.Uri.parse(globPattern.baseUri), globPattern.pattern),
        exactPath: null,
        baseName: null,
      };
    }
    if (typeof globPattern !== 'string' || globPattern.length === 0) {
      return null;
    }
    const hasMeta = /[*?{}\[\]!]/.test(globPattern);
    if (!hasMeta && path.isAbsolute(globPattern)) {
      const normalized = path.normalize(globPattern);
      return {
        pattern: new vscode.RelativePattern(vscode.Uri.file(path.dirname(normalized)), path.basename(normalized)),
        exactPath: normalized,
        baseName: null,
      };
    }
    let baseName = null;
    if (globPattern === '**/lowlight.toml' || globPattern.endsWith('/lowlight.toml') || globPattern.endsWith('\\lowlight.toml')) {
      baseName = 'lowlight.toml';
    } else if (globPattern === '**/lowlight.lock' || globPattern.endsWith('/lowlight.lock') || globPattern.endsWith('\\lowlight.lock')) {
      baseName = 'lowlight.lock';
    }
    return { pattern: globPattern, exactPath: null, baseName };
  }

  /**
   * @param {vscode.Uri} uri
   * @param {number} type
   */
  _notifyWatchedFile(uri, type) {
    this.clearDocCaches();
    if (this.ready) {
      this.sendNotification('workspace/didChangeWatchedFiles', {
        changes: [{ uri: uri.toString(), type }],
      });
    }
  }

  _handleRegisterCapability(reqId, params) {
    const regs = Array.isArray(params && params.registrations) ? params.registrations : [];
    for (const reg of regs) {
      if (!reg || typeof reg.id !== 'string') {
        continue;
      }
      this._disposeRegistration(reg.id);
      if (reg.method !== 'workspace/didChangeWatchedFiles') {
        continue;
      }
      this.hasDynamicFileWatchers = true;
      const watchersSpec = reg.registerOptions && Array.isArray(reg.registerOptions.watchers)
        ? reg.registerOptions.watchers
        : [];
      const disposables = [];
      const exactPaths = new Set();
      const baseNames = new Set();
      for (const spec of watchersSpec) {
        if (!spec) {
          continue;
        }
        const parsed = this._parseWatcherGlob(spec.globPattern);
        if (!parsed) {
          continue;
        }
        if (parsed.exactPath) {
          exactPaths.add(parsed.exactPath);
          try {
            exactPaths.add(fs.realpathSync(parsed.exactPath));
          } catch (_) {}
        }
        if (parsed.baseName) {
          baseNames.add(parsed.baseName);
        }
        const kind = typeof spec.kind === 'number' && spec.kind > 0 ? spec.kind : 7;
        const fsw = vscode.workspace.createFileSystemWatcher(
          parsed.pattern,
          (kind & 1) === 0,
          (kind & 2) === 0,
          (kind & 4) === 0
        );
        disposables.push(fsw);
        if ((kind & 1) !== 0) {
          disposables.push(fsw.onDidCreate((uri) => this._notifyWatchedFile(uri, 1)));
        }
        if ((kind & 2) !== 0) {
          disposables.push(fsw.onDidChange((uri) => this._notifyWatchedFile(uri, 2)));
        }
        if ((kind & 4) !== 0) {
          disposables.push(fsw.onDidDelete((uri) => this._notifyWatchedFile(uri, 3)));
        }
      }
      this.dynamicWatchers.set(reg.id, { disposables, exactPaths, baseNames });
    }
    for (const doc of vscode.workspace.textDocuments) {
      if (!this.syncedUris.has(doc.uri.toString())) {
        this.didOpen(doc);
      }
    }
    if (reqId !== undefined && reqId !== null) {
      this._writeFrame({ jsonrpc: '2.0', id: reqId, result: null });
    }
  }

  _handleUnregisterCapability(reqId, params) {
    const list = Array.isArray(params && params.unregisterations)
      ? params.unregisterations
      : (Array.isArray(params && params.unregistrations) ? params.unregistrations : []);
    for (const item of list) {
      if (item && typeof item.id === 'string') {
        this._disposeRegistration(item.id);
      }
    }
    if (this.dynamicWatchers.size === 0) {
      this.hasDynamicFileWatchers = false;
    }
    if (reqId !== undefined && reqId !== null) {
      this._writeFrame({ jsonrpc: '2.0', id: reqId, result: null });
    }
  }

  async _handleShowDocument(reqId, params) {
    let success = false;
    try {
      if (params && params.uri) {
        const targetUri = this.toVsUri(params.uri);
        const doc = await vscode.workspace.openTextDocument(targetUri);
        const opts = {
          preview: false,
          preserveFocus: params.takeFocus === false,
        };
        if (params.selection) {
          opts.selection = toVsRange(params.selection);
        }
        await vscode.window.showTextDocument(doc, opts);
        success = true;
      }
    } catch (err) {
      this.log(`window/showDocument error: ${err && err.message ? err.message : String(err)}`);
    }
    if (reqId !== undefined && reqId !== null) {
      this._writeFrame({ jsonrpc: '2.0', id: reqId, result: { success } });
    }
  }

  /**
   * @param {any} payload
   */
  _writeFrame(payload) {
    if (!this.proc || !this.proc.stdin || this.proc.stdin.destroyed) {
      throw new Error('Lowlight LSP stdin is not writable');
    }
    const jsonText = JSON.stringify(payload);
    if (this.isTraceEnabled()) {
      this.log(`-> ${jsonText}`);
    }
    const body = Buffer.from(jsonText, 'utf8');
    const header = Buffer.from(`Content-Length: ${body.length}\r\n\r\n`, 'ascii');
    this.proc.stdin.write(Buffer.concat([header, body]));
  }

  /**
   * Sends a JSON-RPC request and awaits its response.
   * @param {string} method
   * @param {any} params
   * @returns {Promise<any>}
   */
  sendRequest(method, params) {
    const id = this.nextId++;
    const payload = {
      jsonrpc: '2.0',
      id,
      method,
      params,
    };

    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, method });
      try {
        this._writeFrame(payload);
      } catch (err) {
        this.pending.delete(id);
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    });
  }

  /**
   * Sends a JSON-RPC notification (no response expected).
   * @param {string} method
   * @param {any} params
   */
  sendNotification(method, params) {
    const payload = {
      jsonrpc: '2.0',
      method,
      params,
    };
    try {
      this._writeFrame(payload);
    } catch (err) {
      this.log(`Notification "${method}" failed: ${err}`);
    }
  }

  /**
   * @param {vscode.TextDocument} doc
   * @returns {boolean}
   */
  isLowlightDoc(doc) {
    return Boolean(doc && doc.languageId === 'lowlight' && doc.uri.scheme === 'file');
  }

  /**
   * @param {string} fsPath
   * @returns {boolean}
   */
  isWatchedNonLowlightPath(fsPath) {
    if (!fsPath) {
      return false;
    }
    const base = path.basename(fsPath);
    if (base === 'lowlight.toml' || base === 'lowlight.lock' || fsPath.endsWith('.lowlightfmt')) {
      return true;
    }
    const norm = path.normalize(fsPath);
    let real = null;
    try {
      real = fs.realpathSync(norm);
    } catch (_) {}
    for (const entry of this.dynamicWatchers.values()) {
      if (!entry) {
        continue;
      }
      if (entry.baseNames && entry.baseNames.has(base)) {
        return true;
      }
      if (entry.exactPaths && (entry.exactPaths.has(norm) || (real && entry.exactPaths.has(real)))) {
        return true;
      }
    }
    return false;
  }

  /**
   * @param {vscode.TextDocument} doc
   * @returns {boolean}
   */
  shouldSyncDoc(doc) {
    if (this.isLowlightDoc(doc)) {
      return true;
    }
    return Boolean(doc && doc.uri && doc.uri.scheme === 'file' && this.isWatchedNonLowlightPath(doc.uri.fsPath));
  }

  /**
   * Resolves a server URI string back into the canonical `vscode.Uri` (reusing open tab URIs).
   * @param {string} uriStr
   * @returns {vscode.Uri}
   */
  toVsUri(uriStr) {
    const parsed = vscode.Uri.parse(uriStr);
    const normalized = path.normalize(parsed.fsPath);
    const existing = this.fsPathToUri.get(normalized);
    return existing || parsed;
  }

  /**
   * @param {vscode.TextDocument} doc
   */
  didOpen(doc) {
    if (!this.ready || !this.shouldSyncDoc(doc)) {
      return;
    }
    const uriStr = doc.uri.toString();
    const isLL = this.isLowlightDoc(doc);
    this.docCache.delete(uriStr);
    if (isLL) {
      this.fsPathToUri.set(path.normalize(doc.uri.fsPath), doc.uri);
    }
    this.syncedUris.add(uriStr);
    this.sendNotification('textDocument/didOpen', {
      textDocument: {
        uri: uriStr,
        languageId: isLL ? 'lowlight' : (doc.languageId || 'plaintext'),
        version: doc.version,
        text: doc.getText(),
      },
    });
  }

  /**
   * @param {vscode.TextDocumentChangeEvent | vscode.TextDocument} docOrEvent
   */
  didChange(docOrEvent) {
    const isEvent = docOrEvent && docOrEvent.document !== undefined;
    const doc = isEvent ? docOrEvent.document : docOrEvent;
    if (!this.ready || !this.shouldSyncDoc(doc)) {
      return;
    }
    const uriStr = doc.uri.toString();
    if (!this.syncedUris.has(uriStr)) {
      this.didOpen(doc);
      return;
    }
    this.docCache.delete(uriStr);
    for (const entry of this.docCache.values()) {
      entry.codeLensesPromise = null;
    }
    const rawChanges = isEvent && Array.isArray(docOrEvent.contentChanges) && docOrEvent.contentChanges.length > 0
      ? docOrEvent.contentChanges.map((ch) => ({
          range: fromVsRange(ch.range),
          rangeLength: ch.rangeLength,
          text: ch.text,
        }))
      : [{ text: doc.getText() }];

    this.sendNotification('textDocument/didChange', {
      textDocument: {
        uri: uriStr,
        version: doc.version,
      },
      contentChanges: rawChanges,
    });
  }

  /**
   * @param {vscode.TextDocument} doc
   */
  didSave(doc) {
    if (!this.ready || !this.shouldSyncDoc(doc)) {
      return;
    }
    const uriStr = doc.uri.toString();
    if (!this.syncedUris.has(uriStr)) {
      this.didOpen(doc);
      return;
    }
    this.sendNotification('textDocument/didSave', {
      textDocument: {
        uri: uriStr,
      },
      text: doc.getText(),
    });
  }

  /**
   * @param {vscode.TextDocument} doc
   */
  didClose(doc) {
    const uriStr = doc && doc.uri ? doc.uri.toString() : '';
    if (!this.shouldSyncDoc(doc) && !(uriStr && this.syncedUris.has(uriStr))) {
      return;
    }
    const isLL = this.isLowlightDoc(doc);
    this.syncedUris.delete(uriStr);
    this.docCache.delete(uriStr);
    if (isLL) {
      this.fsPathToUri.delete(path.normalize(doc.uri.fsPath));
      this.diagnostics.delete(doc.uri);
    }
    if (this.ready) {
      this.sendNotification('textDocument/didClose', {
        textDocument: {
          uri: uriStr,
        },
      });
    }
  }

  /**
   * Ensures the server is running and `doc` has been opened on the server.
   * @param {vscode.TextDocument} doc
   * @returns {Promise<boolean>}
   */
  async ensureReadyForDoc(doc) {
    if (!this.isLowlightDoc(doc)) {
      return false;
    }
    if (!this.ready) {
      const ok = await this.start();
      if (!ok) {
        return false;
      }
    }
    const uriStr = doc.uri.toString();
    if (!this.syncedUris.has(uriStr)) {
      this.didOpen(doc);
    }
    return true;
  }

  /**
   * Converts incoming `textDocument/publishDiagnostics` notifications into VS Code squiggles.
   * @param {{ uri: string, diagnostics: Array<any> }} params
   */
  _handleDiagnostics(params) {
    if (!params || typeof params.uri !== 'string') {
      return;
    }
    const targetUri = this.toVsUri(params.uri);
    const rawList = Array.isArray(params.diagnostics) ? params.diagnostics : [];
    const vsDiags = rawList.map((d) => {
      const range = toVsRange(d.range);
      const sev = toVsSeverity(d.severity);
      const diag = new vscode.Diagnostic(range, d.message || '', sev);
      diag.source = d.source || 'lowlight';
      if (d.code !== undefined && d.code !== null && d.code !== '') {
        diag.code = d.code;
      }
      return diag;
    });
    this.diagnostics.set(targetUri, vsDiags);
  }
}

// -----------------------------------------------------------------------------
// Coordinate & Type Conversion Helpers (LSP 3.17 <-> VS Code API)
// -----------------------------------------------------------------------------

/**
 * @param {{ line: number, character: number } | undefined} p
 * @returns {vscode.Position}
 */
function toVsPosition(p) {
  if (!p) {
    return new vscode.Position(0, 0);
  }
  return new vscode.Position(Math.max(0, p.line || 0), Math.max(0, p.character || 0));
}

/**
 * @param {{ start: { line: number, character: number }, end: { line: number, character: number } } | undefined} r
 * @returns {vscode.Range}
 */
function toVsRange(r) {
  if (!r) {
    return new vscode.Range(0, 0, 0, 0);
  }
  return new vscode.Range(toVsPosition(r.start), toVsPosition(r.end));
}

/**
 * @param {vscode.Position} pos
 * @returns {{ line: number, character: number }}
 */
function fromVsPosition(pos) {
  return { line: pos.line, character: pos.character };
}

/**
 * @param {vscode.Range} range
 * @returns {{ start: { line: number, character: number }, end: { line: number, character: number } }}
 */
function fromVsRange(range) {
  return {
    start: fromVsPosition(range.start),
    end: fromVsPosition(range.end),
  };
}

/**
 * @param {number | undefined} sev
 * @returns {vscode.DiagnosticSeverity}
 */
function toVsSeverity(sev) {
  switch (sev) {
    case 1:
      return vscode.DiagnosticSeverity.Error;
    case 2:
      return vscode.DiagnosticSeverity.Warning;
    case 3:
      return vscode.DiagnosticSeverity.Information;
    case 4:
      return vscode.DiagnosticSeverity.Hint;
    default:
      return vscode.DiagnosticSeverity.Error;
  }
}

/**
 * @param {vscode.DiagnosticSeverity} sev
 * @returns {number}
 */
function fromVsSeverity(sev) {
  switch (sev) {
    case vscode.DiagnosticSeverity.Error:
      return 1;
    case vscode.DiagnosticSeverity.Warning:
      return 2;
    case vscode.DiagnosticSeverity.Information:
      return 3;
    case vscode.DiagnosticSeverity.Hint:
      return 4;
    default:
      return 1;
  }
}

/**
 * @param {{ kind?: string, value?: string } | string | undefined} doc
 * @returns {vscode.MarkdownString | string | undefined}
 */
function toVsDocumentation(doc) {
  if (!doc) {
    return undefined;
  }
  if (typeof doc === 'string') {
    return doc;
  }
  if (doc.value) {
    if (doc.kind === 'markdown') {
      const ms = new vscode.MarkdownString(doc.value);
      ms.isTrusted = true;
      return ms;
    }
    return doc.value;
  }
  return undefined;
}

/**
 * Recursively converts an LSP `DocumentSymbol` into a `vscode.DocumentSymbol`.
 * @param {any} sym
 * @returns {vscode.DocumentSymbol}
 */
function toVsDocumentSymbol(sym) {
  const kind = typeof sym.kind === 'number' && sym.kind >= 1
    ? sym.kind - 1
    : vscode.SymbolKind.Variable;
  const selRange = toVsRange(sym.selectionRange || sym.range);
  const fullRange = toVsRange(sym.range || sym.selectionRange).union(selRange);
  const vsSym = new vscode.DocumentSymbol(
    sym.name || '<anonymous>',
    sym.detail || '',
    kind,
    fullRange,
    selRange
  );
  if (Array.isArray(sym.children) && sym.children.length > 0) {
    vsSym.children = sym.children.map(toVsDocumentSymbol);
  }
  return vsSym;
}

// -----------------------------------------------------------------------------
// Extension Lifecycle (`activate` / `deactivate`)
// -----------------------------------------------------------------------------

/**
 * @param {vscode.ExtensionContext} context
 */
function activate(context) {
  outputChannel = vscode.window.createOutputChannel('Lowlight Language Server');
  diagnosticCollection = vscode.languages.createDiagnosticCollection('lowlight');
  statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  statusBarItem.command = 'lowlight.showOutput';

  client = new LowlightClient(context, outputChannel, diagnosticCollection, statusBarItem);
  client.start();

  const testController = createLowlightTestController(context, {
    resolveServerBinary,
    resolveStdlibRoot,
    log: (msg) => {
      if (outputChannel) {
        const ts = new Date().toISOString().slice(11, 23);
        outputChannel.appendLine(`[${ts}] ${msg}`);
      }
    },
  });

  const selector = [{ language: 'lowlight', scheme: 'file' }];

  const inlayHintsEmitter = new vscode.EventEmitter();
  const watcher = vscode.workspace.createFileSystemWatcher('**/*.{ll,lowlightfmt}');
  const tomlWatcher = vscode.workspace.createFileSystemWatcher('**/lowlight.{toml,lock}');
  const sendFallbackWatchEvent = (uri, type) => {
    if (!client) {
      return;
    }
    client.clearDocCaches();
    if (client.ready && !client.hasDynamicFileWatchers) {
      client.sendNotification('workspace/didChangeWatchedFiles', {
        changes: [{ uri: uri.toString(), type }],
      });
    }
  };

  // Document synchronization listeners
  context.subscriptions.push(
    outputChannel,
    diagnosticCollection,
    statusBarItem,
    inlayHintsEmitter,
    watcher,
    tomlWatcher,
    vscode.workspace.onDidOpenTextDocument((doc) => client && client.didOpen(doc)),
    vscode.workspace.onDidChangeTextDocument((ev) => client && client.didChange(ev)),
    vscode.workspace.onDidSaveTextDocument((doc) => client && client.didSave(doc)),
    vscode.workspace.onDidCloseTextDocument((doc) => client && client.didClose(doc)),
    watcher.onDidCreate((uri) => sendFallbackWatchEvent(uri, 1)),
    watcher.onDidChange((uri) => sendFallbackWatchEvent(uri, 2)),
    watcher.onDidDelete((uri) => sendFallbackWatchEvent(uri, 3)),
    tomlWatcher.onDidCreate((uri) => sendFallbackWatchEvent(uri, 1)),
    tomlWatcher.onDidChange((uri) => sendFallbackWatchEvent(uri, 2)),
    tomlWatcher.onDidDelete((uri) => sendFallbackWatchEvent(uri, 3)),
    vscode.workspace.onDidChangeConfiguration(async (ev) => {
      if (
        ev.affectsConfiguration('lowlight.serverPath') ||
        ev.affectsConfiguration('lowlight.stdlibPath')
      ) {
        if (client) {
          client.clearDocCaches();
          await client.stop();
          await client.start();
        }
        return;
      }
      if (
        ev.affectsConfiguration('lowlight.format') ||
        ev.affectsConfiguration('lowlight.inlayHints') ||
        ev.affectsConfiguration('lowlight.target')
      ) {
        if (client && client.ready) {
          client.clearDocCaches();
          client.sendNotification('workspace/didChangeConfiguration', {
            settings: { lowlight: getLspSettings() },
          });
          inlayHintsEmitter.fire();
        }
      }
    })
  );

  // 1. Context-Aware Autocomplete Provider
  context.subscriptions.push(
    vscode.languages.registerCompletionItemProvider(
      selector,
      {
        async provideCompletionItems(document, position, token, completionContext) {
          if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
            return null;
          }
          const res = await client.sendRequest('textDocument/completion', {
            textDocument: { uri: document.uri.toString() },
            position: fromVsPosition(position),
            context: {
              triggerKind: (completionContext && completionContext.triggerKind ? completionContext.triggerKind : 0) + 1,
              triggerCharacter: completionContext ? completionContext.triggerCharacter : undefined,
            },
          });
          if (!res) {
            return null;
          }
          const rawItems = Array.isArray(res) ? res : (Array.isArray(res.items) ? res.items : []);
          const items = rawItems.map((item) => {
            const kind = typeof item.kind === 'number' && item.kind >= 1
              ? item.kind - 1
              : vscode.CompletionItemKind.Text;
            const ci = new vscode.CompletionItem(item.label, kind);
            if (item.detail) {
              ci.detail = item.detail;
            }
            const doc = toVsDocumentation(item.documentation);
            if (doc) {
              ci.documentation = doc;
            }
            if (item.sortText) {
              ci.sortText = item.sortText;
            }
            if (item.filterText) {
              ci.filterText = item.filterText;
            }
            const rawInsert = (item.textEdit && item.textEdit.newText !== undefined)
              ? item.textEdit.newText
              : (item.insertText !== undefined ? item.insertText : item.label);
            if (item.insertTextFormat === 2) {
              ci.insertText = new vscode.SnippetString(rawInsert);
            } else {
              ci.insertText = rawInsert;
            }
            if (item.textEdit && item.textEdit.range) {
              ci.range = toVsRange(item.textEdit.range);
            }
            if (Array.isArray(item.additionalTextEdits) && item.additionalTextEdits.length > 0) {
              ci.additionalTextEdits = item.additionalTextEdits.map((ed) =>
                vscode.TextEdit.replace(toVsRange(ed.range), ed.newText || '')
              );
            }
            return ci;
          });
          return new vscode.CompletionList(items, Boolean(res.isIncomplete));
        },
      },
      '.', '?', '[', ':', '"', '/'
    )
  );

  // 2. Hover Type & Ownership Inspector
  context.subscriptions.push(
    vscode.languages.registerHoverProvider(selector, {
      async provideHover(document, position, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return null;
        }
        const res = await client.sendRequest('textDocument/hover', {
          textDocument: { uri: document.uri.toString() },
          position: fromVsPosition(position),
        });
        if (!res || !res.contents) {
          return null;
        }
        const doc = toVsDocumentation(res.contents);
        if (!doc) {
          return null;
        }
        return new vscode.Hover(doc, res.range ? toVsRange(res.range) : undefined);
      },
    })
  );

  // 3. Cross-Module Go-to-Definition
  context.subscriptions.push(
    vscode.languages.registerDefinitionProvider(selector, {
      async provideDefinition(document, position, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return null;
        }
        const res = await client.sendRequest('textDocument/definition', {
          textDocument: { uri: document.uri.toString() },
          position: fromVsPosition(position),
        });
        if (!res) {
          return null;
        }
        const list = Array.isArray(res) ? res : [res];
        return list
          .filter((loc) => loc && loc.uri && loc.range)
          .map((loc) => new vscode.Location(client.toVsUri(loc.uri), toVsRange(loc.range)));
      },
    })
  );

  // 4. UFCS-Aware Signature Help
  context.subscriptions.push(
    vscode.languages.registerSignatureHelpProvider(
      selector,
      {
        async provideSignatureHelp(document, position, token) {
          if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
            return null;
          }
          const res = await client.sendRequest('textDocument/signatureHelp', {
            textDocument: { uri: document.uri.toString() },
            position: fromVsPosition(position),
          });
          if (!res || !Array.isArray(res.signatures) || res.signatures.length === 0) {
            return null;
          }
          const sh = new vscode.SignatureHelp();
          sh.signatures = res.signatures.map((sig) => {
            const si = new vscode.SignatureInformation(
              sig.label,
              toVsDocumentation(sig.documentation)
            );
            if (Array.isArray(sig.parameters)) {
              si.parameters = sig.parameters.map(
                (p) => new vscode.ParameterInformation(p.label, toVsDocumentation(p.documentation))
              );
            }
            return si;
          });
          sh.activeSignature = typeof res.activeSignature === 'number' ? res.activeSignature : 0;
          sh.activeParameter = typeof res.activeParameter === 'number' ? res.activeParameter : 0;
          return sh;
        },
      },
      '(', ','
    )
  );

  // 5. Hierarchical Document Symbols (Outline & Breadcrumbs)
  context.subscriptions.push(
    vscode.languages.registerDocumentSymbolProvider(selector, {
      async provideDocumentSymbols(document, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const cache = client.getDocCache(document);
        if (!cache.documentSymbolsPromise) {
          const p = client.sendRequest('textDocument/documentSymbol', {
            textDocument: { uri: document.uri.toString() },
          }).then((res) => (Array.isArray(res) ? res.map(toVsDocumentSymbol) : [])).catch((err) => {
            if (cache.documentSymbolsPromise === p) {
              cache.documentSymbolsPromise = null;
            }
            throw err;
          });
          cache.documentSymbolsPromise = p;
        }
        const syms = await cache.documentSymbolsPromise;
        if (token.isCancellationRequested) {
          return [];
        }
        return syms;
      },
    })
  );

  // 6. Quick-Fix Code Actions (`Cmd+.` / `Ctrl+.`)
  context.subscriptions.push(
    vscode.languages.registerCodeActionsProvider(
      selector,
      {
        async provideCodeActions(document, range, codeActionContext, token) {
          if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
            return [];
          }
          const lspDiags = (codeActionContext && Array.isArray(codeActionContext.diagnostics))
            ? codeActionContext.diagnostics.map((d) => ({
                range: fromVsRange(d.range),
                severity: fromVsSeverity(d.severity),
                code: typeof d.code === 'string' ? d.code : '',
                source: d.source || 'lowlight',
                message: d.message || '',
              }))
            : [];

          const onlyKinds = codeActionContext && codeActionContext.only
            ? [codeActionContext.only.value]
            : undefined;
          const res = await client.sendRequest('textDocument/codeAction', {
            textDocument: { uri: document.uri.toString() },
            range: fromVsRange(range),
            context: {
              diagnostics: lspDiags,
              only: onlyKinds,
            },
          });
          if (!Array.isArray(res)) {
            return [];
          }
          return res.map((act) => {
            const kind = act.kind ? new vscode.CodeActionKind(act.kind) : vscode.CodeActionKind.QuickFix;
            const ca = new vscode.CodeAction(act.title, kind);
            ca.isPreferred = Boolean(act.isPreferred);
            if (act.edit && act.edit.changes) {
              const wsEdit = new vscode.WorkspaceEdit();
              for (const [editUriStr, edits] of Object.entries(act.edit.changes)) {
                const targetUri = client.toVsUri(editUriStr);
                if (Array.isArray(edits)) {
                  for (const te of edits) {
                    wsEdit.replace(targetUri, toVsRange(te.range), te.newText || '');
                  }
                }
              }
              ca.edit = wsEdit;
            }
            if (act.command && act.command.command) {
              ca.command = {
                title: act.command.title || act.title,
                command: act.command.command,
                arguments: Array.isArray(act.command.arguments) ? act.command.arguments : [],
              };
            }
            return ca;
          });
        },
      },
      {
        providedCodeActionKinds: [
          vscode.CodeActionKind.QuickFix,
          vscode.CodeActionKind.Refactor,
          vscode.CodeActionKind.RefactorExtract,
          vscode.CodeActionKind.RefactorRewrite,
          vscode.CodeActionKind.Source,
        ],
      }
    )
  );

  // 6b. Go to Implementation (`Cmd+F12` / `Ctrl+F12`)
  context.subscriptions.push(
    vscode.languages.registerImplementationProvider(selector, {
      async provideImplementation(document, position, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const res = await client.sendRequest('textDocument/implementation', {
          textDocument: { uri: document.uri.toString() },
          position: fromVsPosition(position),
        });
        if (!res) {
          return [];
        }
        const locs = Array.isArray(res) ? res : [res];
        return locs
          .filter((loc) => loc && loc.uri && loc.range)
          .map((loc) => new vscode.Location(client.toVsUri(loc.uri), toVsRange(loc.range)));
      },
    })
  );

  // 7. Document Formatting (`Shift+Alt+F`)
  context.subscriptions.push(
    vscode.languages.registerDocumentFormattingEditProvider(selector, {
      async provideDocumentFormattingEdits(document, options, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const res = await client.sendRequest('textDocument/formatting', {
          textDocument: { uri: document.uri.toString() },
          options: {
            tabSize: options.tabSize || 4,
            insertSpaces: options.insertSpaces !== false,
          },
        });
        if (!Array.isArray(res)) {
          return [];
        }
        return res.map((te) => new vscode.TextEdit(toVsRange(te.range), te.newText || ''));
      },
    })
  );

  // 8. Inlay Hints (Type & Parameter Name hints)
  context.subscriptions.push(
    vscode.languages.registerInlayHintsProvider(selector, {
      onDidChangeInlayHints: inlayHintsEmitter.event,
      async provideInlayHints(document, range, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const cache = client.getDocCache(document);
        if (!cache.inlayHintsPromise) {
          const lastLine = Math.max(0, document.lineCount - 1);
          const lastChar = document.lineAt(lastLine).text.length;
          const p = client.sendRequest('textDocument/inlayHint', {
            textDocument: { uri: document.uri.toString() },
            range: {
              start: { line: 0, character: 0 },
              end: { line: lastLine, character: lastChar },
            },
          }).then((res) => {
            if (!Array.isArray(res)) {
              return [];
            }
            return res.map((hint) => {
              const vh = new vscode.InlayHint(
                toVsPosition(hint.position),
                hint.label,
                hint.kind === 1 ? vscode.InlayHintKind.Type : vscode.InlayHintKind.Parameter
              );
              vh.paddingLeft = Boolean(hint.paddingLeft);
              vh.paddingRight = Boolean(hint.paddingRight);
              return vh;
            });
          }).catch((err) => {
            if (cache.inlayHintsPromise === p) {
              cache.inlayHintsPromise = null;
            }
            throw err;
          });
          cache.inlayHintsPromise = p;
        }
        const allHints = await cache.inlayHintsPromise;
        if (token.isCancellationRequested || !allHints || allHints.length === 0) {
          return [];
        }
        return allHints.filter((vh) => range.contains(vh.position));
      },
    })
  );

  // 9. Rename Symbol (`F2`)
  context.subscriptions.push(
    vscode.languages.registerRenameProvider(selector, {
      async prepareRename(document, position, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return null;
        }
        const res = await client.sendRequest('textDocument/prepareRename', {
          textDocument: { uri: document.uri.toString() },
          position: fromVsPosition(position),
        });
        if (!res || !res.start || !res.end) {
          return null;
        }
        return toVsRange(res);
      },
      async provideRenameEdits(document, position, newName, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return null;
        }
        const res = await client.sendRequest('textDocument/rename', {
          textDocument: { uri: document.uri.toString() },
          position: fromVsPosition(position),
          newName,
        });
        if (!res || !res.changes) {
          return null;
        }
        const wsEdit = new vscode.WorkspaceEdit();
        for (const [editUriStr, edits] of Object.entries(res.changes)) {
          const targetUri = client.toVsUri(editUriStr);
          if (Array.isArray(edits)) {
            for (const te of edits) {
              wsEdit.replace(targetUri, toVsRange(te.range), te.newText || '');
            }
          }
        }
        return wsEdit;
      },
    })
  );

  // 10. Test Code Lenses (`▶ Run Test`, `▶ Run All Tests in File`)
  context.subscriptions.push(
    vscode.languages.registerCodeLensProvider(selector, {
      async provideCodeLenses(document, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const cache = client.getDocCache(document);
        if (!cache.codeLensesPromise) {
          const p = client.sendRequest('textDocument/codeLens', {
            textDocument: { uri: document.uri.toString() },
          }).then((res) => {
            if (!Array.isArray(res)) {
              return [];
            }
            return res.map((cl) => {
              const lens = new vscode.CodeLens(toVsRange(cl.range));
              if (cl.command) {
                lens.command = {
                  title: cl.command.title,
                  command: cl.command.command,
                  arguments: cl.command.arguments || [],
                };
              }
              return lens;
            });
          }).catch((err) => {
            if (cache.codeLensesPromise === p) {
              cache.codeLensesPromise = null;
            }
            throw err;
          });
          cache.codeLensesPromise = p;
        }
        const lenses = await cache.codeLensesPromise;
        if (token.isCancellationRequested) {
          return [];
        }
        return lenses;
      },
    })
  );

  // 11. Cross-File Find All References (`Shift+F12`)
  context.subscriptions.push(
    vscode.languages.registerReferenceProvider(selector, {
      async provideReferences(document, position, refContext, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const res = await client.sendRequest('textDocument/references', {
          textDocument: { uri: document.uri.toString() },
          position: fromVsPosition(position),
          context: { includeDeclaration: refContext ? Boolean(refContext.includeDeclaration) : true },
        });
        if (!Array.isArray(res)) {
          return [];
        }
        return res
          .filter((loc) => loc && loc.uri && loc.range)
          .map((loc) => new vscode.Location(client.toVsUri(loc.uri), toVsRange(loc.range)));
      },
    })
  );

  // 12. Go to Type Definition & Declaration
  context.subscriptions.push(
    vscode.languages.registerTypeDefinitionProvider(selector, {
      async provideTypeDefinition(document, position, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const res = await client.sendRequest('textDocument/typeDefinition', {
          textDocument: { uri: document.uri.toString() },
          position: fromVsPosition(position),
        });
        if (!res) {
          return [];
        }
        const list = Array.isArray(res) ? res : [res];
        return list
          .filter((loc) => loc && loc.uri && loc.range)
          .map((loc) => new vscode.Location(client.toVsUri(loc.uri), toVsRange(loc.range)));
      },
    }),
    vscode.languages.registerDeclarationProvider(selector, {
      async provideDeclaration(document, position, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const res = await client.sendRequest('textDocument/declaration', {
          textDocument: { uri: document.uri.toString() },
          position: fromVsPosition(position),
        });
        if (!res) {
          return [];
        }
        const list = Array.isArray(res) ? res : [res];
        return list
          .filter((loc) => loc && loc.uri && loc.range)
          .map((loc) => new vscode.Location(client.toVsUri(loc.uri), toVsRange(loc.range)));
      },
    })
  );

  // 13. Workspace Symbol Search (`Cmd+T` / `Ctrl+T`)
  context.subscriptions.push(
    vscode.languages.registerWorkspaceSymbolProvider({
      async provideWorkspaceSymbols(query, token) {
        if (!client || !client.ready || token.isCancellationRequested) {
          return [];
        }
        const res = await client.sendRequest('workspace/symbol', { query: query || '' });
        if (!Array.isArray(res)) {
          return [];
        }
        return res
          .filter((sym) => sym && sym.location && sym.location.uri && sym.location.range)
          .map((sym) => {
            const kind = typeof sym.kind === 'number' && sym.kind >= 1
              ? sym.kind - 1
              : vscode.SymbolKind.Variable;
            return new vscode.SymbolInformation(
              sym.name || '<anonymous>',
              kind,
              sym.containerName || '',
              new vscode.Location(client.toVsUri(sym.location.uri), toVsRange(sym.location.range))
            );
          });
      },
    })
  );

  // 14. Folding & Selection Ranges
  context.subscriptions.push(
    vscode.languages.registerFoldingRangeProvider(selector, {
      async provideFoldingRanges(document, _ctx, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const cache = client.getDocCache(document);
        if (!cache.foldingRangesPromise) {
          const p = client.sendRequest('textDocument/foldingRange', {
            textDocument: { uri: document.uri.toString() },
          }).then((res) => {
            if (!Array.isArray(res)) {
              return [];
            }
            return res.map((fr) => {
              let kind = vscode.FoldingRangeKind.Region;
              if (fr.kind === 'comment') {
                kind = vscode.FoldingRangeKind.Comment;
              } else if (fr.kind === 'imports') {
                kind = vscode.FoldingRangeKind.Imports;
              }
              return new vscode.FoldingRange(fr.startLine, fr.endLine, kind);
            });
          }).catch((err) => {
            if (cache.foldingRangesPromise === p) {
              cache.foldingRangesPromise = null;
            }
            throw err;
          });
          cache.foldingRangesPromise = p;
        }
        const ranges = await cache.foldingRangesPromise;
        if (token.isCancellationRequested) {
          return [];
        }
        return ranges;
      },
    }),
    vscode.languages.registerSelectionRangeProvider(selector, {
      async provideSelectionRanges(document, positions, token) {
        if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
          return [];
        }
        const res = await client.sendRequest('textDocument/selectionRange', {
          textDocument: { uri: document.uri.toString() },
          positions: positions.map(fromVsPosition),
        });
        if (!Array.isArray(res)) {
          return [];
        }
        const convert = (sr) => {
          if (!sr || !sr.range) {
            return undefined;
          }
          return new vscode.SelectionRange(toVsRange(sr.range), convert(sr.parent));
        };
        return res.map(convert).filter(Boolean);
      },
    })
  );

  // 15. Semantic Tokens (`textDocument/semanticTokens/full`)
  const semanticLegend = new vscode.SemanticTokensLegend(
    [
      'namespace', 'type', 'struct', 'enum', 'interface', 'typeParameter',
      'parameter', 'variable', 'property', 'enumMember', 'function',
      'method', 'keyword', 'comment', 'string', 'number', 'operator',
    ],
    [
      'declaration', 'definition', 'readonly', 'static',
      'documentation', 'modification', 'defaultLibrary',
    ]
  );
  context.subscriptions.push(
    vscode.languages.registerDocumentSemanticTokensProvider(
      selector,
      {
        async provideDocumentSemanticTokens(document, token) {
          if (!client || !(await client.ensureReadyForDoc(document)) || token.isCancellationRequested) {
            return null;
          }
          const cache = client.getDocCache(document);
          if (!cache.semanticTokensPromise) {
            const p = client.sendRequest('textDocument/semanticTokens/full', {
              textDocument: { uri: document.uri.toString() },
            }).then((res) => {
              if (!res || !Array.isArray(res.data)) {
                return null;
              }
              return new vscode.SemanticTokens(new Uint32Array(res.data));
            }).catch((err) => {
              if (cache.semanticTokensPromise === p) {
                cache.semanticTokensPromise = null;
              }
              throw err;
            });
            cache.semanticTokensPromise = p;
          }
          const tokens = await cache.semanticTokensPromise;
          if (token.isCancellationRequested) {
            return null;
          }
          return tokens;
        },
      },
      semanticLegend
    )
  );

  // 7. User Commands (`Restart Server`, `Run Current File`, `Show Output`)
  context.subscriptions.push(
    vscode.commands.registerCommand('lowlight.goToLocation', async (...args) => {
      if (args.length === 0) return;
      let uriStr, line = 0, char = 0;
      const first = args[0];
      if (typeof first === 'object' && first !== null) {
        uriStr = first.uri;
        if (first.range && first.range.start) {
          line = first.range.start.line || 0;
          char = first.range.start.character || 0;
        }
      } else if (typeof first === 'string') {
        uriStr = first;
        line = typeof args[1] === 'number' ? args[1] : 0;
        char = typeof args[2] === 'number' ? args[2] : 0;
      }
      if (!uriStr) return;
      const uri = vscode.Uri.parse(uriStr);
      const doc = await vscode.workspace.openTextDocument(uri);
      const editor = await vscode.window.showTextDocument(doc);
      const pos = new vscode.Position(line, char);
      editor.selection = new vscode.Selection(pos, pos);
      editor.revealRange(new vscode.Range(pos, pos), vscode.TextEditorRevealType.InCenter);
    }),

    vscode.commands.registerCommand('lowlight.showLocations', async (currentUriStr, posObj, locsList) => {
      if (!locsList || locsList.length === 0) return;
      if (locsList.length === 1) {
        return vscode.commands.executeCommand('lowlight.goToLocation', locsList[0]);
      }
      const currentUri = typeof currentUriStr === 'string' ? vscode.Uri.parse(currentUriStr) : currentUriStr;
      const pos = new vscode.Position((posObj && posObj.line) || 0, (posObj && posObj.character) || 0);
      const vscodeLocs = locsList.map(l => {
        const u = vscode.Uri.parse(l.uri);
        const r = new vscode.Range(
          new vscode.Position(l.range.start.line, l.range.start.character),
          new vscode.Position(l.range.end.line, l.range.end.character)
        );
        return new vscode.Location(u, r);
      });
      return vscode.commands.executeCommand('editor.action.goToLocations', currentUri, pos, vscodeLocs, 'goto', 'No implementations found');
    }),

    vscode.commands.registerCommand('lowlight.showImplementationsQuickPick', async (title, items) => {
      if (!items || items.length === 0) {
        vscode.window.showInformationMessage('No implementations found.');
        return;
      }
      if (items.length === 1 && items[0].uri) {
        return vscode.commands.executeCommand('lowlight.goToLocation', items[0]);
      }
      const pick = await vscode.window.showQuickPick(items, {
        placeHolder: title || 'Select target to navigate to...',
        matchOnDescription: true,
      });
      if (pick) {
        if (pick.uri) {
          return vscode.commands.executeCommand('lowlight.goToLocation', pick);
        } else {
          vscode.window.showInformationMessage(`${pick.label} is a built-in Lowlight primitive.`);
        }
      }
    }),

    vscode.commands.registerCommand('lowlight.restartServer', async () => {
      if (!client) {
        return;
      }
      outputChannel.appendLine('Restarting Lowlight Language Server...');
      await client.stop();
      const ok = await client.start();
      if (ok) {
        vscode.window.setStatusBarMessage('Lowlight Language Server restarted', 3000);
      }
    }),

    vscode.commands.registerCommand('lowlight.showOutput', () => {
      if (outputChannel) {
        outputChannel.show(true);
      }
    }),

    vscode.commands.registerCommand('lowlight.runFile', async (arg) => {
      let filePath = '';
      if (typeof arg === 'string' && arg.length > 0) {
        filePath = arg;
        if (filePath.startsWith('file://')) {
          filePath = vscode.Uri.parse(filePath).fsPath;
        }
      } else {
        const editor = vscode.window.activeTextEditor;
        if (editor && editor.document.languageId === 'lowlight') {
          filePath = editor.document.uri.fsPath;
        }
      }
      if (!filePath) {
        vscode.window.showInformationMessage('Open a Lowlight (.ll) file to run it.');
        return;
      }
      const editor = vscode.window.activeTextEditor;
      if (editor && editor.document.isDirty) {
        await editor.document.save();
      }
      const binPath = resolveServerBinary(context);
      const term = getOrCreateRunTerminal('Lowlight');
      term.show(true);
      term.sendText(`${quoteShellArg(binPath)} run ${quoteShellArg(filePath)}`);
    }),

    vscode.commands.registerCommand('lowlight.runMain', async (arg) => {
      return vscode.commands.executeCommand('lowlight.runFile', arg);
    }),

    vscode.commands.registerCommand('lowlight.runTest', async (filePath, testName) => {
      const editor = vscode.window.activeTextEditor;
      let targetFile = filePath || (editor && editor.document.uri.fsPath);
      if (!targetFile) {
        return;
      }
      if (targetFile.startsWith('file://')) {
        targetFile = vscode.Uri.parse(targetFile).fsPath;
      }
      if (editor && editor.document.isDirty) {
        await editor.document.save();
      }
      const useTerminal = Boolean(vscode.workspace.getConfiguration('lowlight').get('test.useTerminal', false));
      if (!useTerminal && testController) {
        await testController.runTestByCommand(targetFile, testName);
        return;
      }
      const binPath = resolveServerBinary(context);
      const term = getOrCreateRunTerminal('Lowlight Test');
      term.show(true);
      const cmd = testName
        ? `${quoteShellArg(binPath)} test ${quoteShellArg(targetFile)} -run ${quoteShellArg('^' + testName + '$')}`
        : `${quoteShellArg(binPath)} test ${quoteShellArg(targetFile)}`;
      term.sendText(cmd);
    }),

    vscode.commands.registerCommand('lowlight.runAllTests', async (filePath) => {
      const editor = vscode.window.activeTextEditor;
      let targetFile = filePath || (editor && editor.document.uri.fsPath);
      if (targetFile && targetFile.startsWith('file://')) {
        targetFile = vscode.Uri.parse(targetFile).fsPath;
      }
      if (editor && editor.document.isDirty) {
        await editor.document.save();
      }
      const useTerminal = Boolean(vscode.workspace.getConfiguration('lowlight').get('test.useTerminal', false));
      if (!useTerminal && testController) {
        await testController.runAllTestsByCommand(targetFile);
        return;
      }
      if (!targetFile) {
        return;
      }
      const binPath = resolveServerBinary(context);
      const term = getOrCreateRunTerminal('Lowlight Test');
      term.show(true);
      term.sendText(`${quoteShellArg(binPath)} test ${quoteShellArg(targetFile)}`);
    }),

    vscode.commands.registerCommand('lowlight.refreshTests', async () => {
      if (testController) {
        await testController.refresh();
      }
    }),

    (() => {
      const expandedDocs = new Map();
      const expandedEmitter = new vscode.EventEmitter();
      context.subscriptions.push(
        expandedEmitter,
        vscode.workspace.registerTextDocumentContentProvider('lowlight-expanded', {
          onDidChange: expandedEmitter.event,
          provideTextDocumentContent(uri) {
            return expandedDocs.get(uri.toString()) || '';
          },
        })
      );

      return vscode.commands.registerCommand('lowlight.expandDocument', async (arg) => {
        const active = vscode.window.activeTextEditor;
        const targetUri = arg && typeof arg.toString === 'function'
          ? arg
          : (active && active.document && active.document.languageId === 'lowlight' ? active.document.uri : null);
        if (!targetUri) {
          vscode.window.showInformationMessage('Open a Lowlight (.ll) file to expand.');
          return;
        }
        const doc = await vscode.workspace.openTextDocument(targetUri);
        if (!client || !(await client.ensureReadyForDoc(doc))) {
          vscode.window.showErrorMessage('Lowlight language server is not ready.');
          return;
        }
        const res = await client.sendRequest('lowlight/expandDocument', {
          textDocument: { uri: doc.uri.toString() },
        });
        if (!res || typeof res.text !== 'string') {
          vscode.window.showErrorMessage('Unable to expand Lowlight document.');
          return;
        }
        const base = path.basename(doc.fileName, path.extname(doc.fileName));
        const header = `// Expanded by Lowlight (target: ${res.target || 'host'})\n`;
        const virtualUri = vscode.Uri.parse(`lowlight-expanded:${encodeURIComponent(base)}.expanded.ll?src=${encodeURIComponent(doc.uri.toString())}`);
        expandedDocs.set(virtualUri.toString(), header + res.text);
        expandedEmitter.fire(virtualUri);
        const expandedDoc = await vscode.workspace.openTextDocument(virtualUri);
        await vscode.languages.setTextDocumentLanguage(expandedDoc, 'lowlight');
        await vscode.window.showTextDocument(expandedDoc, {
          viewColumn: vscode.ViewColumn.Beside,
          preview: true,
          preserveFocus: true,
        });
      });
    })(),

    vscode.commands.registerCommand('lowlight.setTarget', async (tripleArg) => {
      const cfg = vscode.workspace.getConfiguration('lowlight');
      const current = String(cfg.get('target', '') || '').trim();
      let pickedTriple = typeof tripleArg === 'string' ? tripleArg : undefined;
      if (pickedTriple === undefined) {
        const choices = [
          { label: '$(home) Host Default', description: 'Use host / manifest target', value: '' },
          { label: 'x86_64-unknown-linux-gnu', description: 'Linux x86_64 (glibc)', value: 'x86_64-unknown-linux-gnu' },
          { label: 'aarch64-unknown-linux-gnu', description: 'Linux arm64 (glibc)', value: 'aarch64-unknown-linux-gnu' },
          { label: 'aarch64-apple-darwin', description: 'macOS Apple Silicon', value: 'aarch64-apple-darwin' },
          { label: 'x86_64-apple-darwin', description: 'macOS Intel', value: 'x86_64-apple-darwin' },
          { label: 'x86_64-pc-windows-msvc', description: 'Windows x86_64 (MSVC)', value: 'x86_64-pc-windows-msvc' },
          { label: 'wasm32-unknown-linux-gnu', description: 'WebAssembly 32-bit (wasm32)', value: 'wasm32-unknown-unknown' },
          { label: 'riscv64-unknown-linux-gnu', description: 'Linux RISC-V 64', value: 'riscv64-unknown-linux-gnu' },
          { label: '$(edit) Custom target triple...', description: `Current: ${current || 'host default'}`, value: '__custom__' },
        ];
        const pick = await vscode.window.showQuickPick(choices, {
          placeHolder: `Select Lowlight compilation target (current: ${current || 'host default'})`,
        });
        if (!pick) {
          return;
        }
        if (pick.value === '__custom__') {
          const entered = await vscode.window.showInputBox({
            prompt: 'Enter Lowlight target triple (leave blank for host default)',
            value: current,
            placeHolder: 'e.g. x86_64-unknown-linux-gnu or aarch64-apple-darwin',
          });
          if (entered === undefined) {
            return;
          }
          pickedTriple = entered.trim();
        } else {
          pickedTriple = pick.value;
        }
      }
      const nextTriple = String(pickedTriple || '').trim();
      const targetScope = (vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders.length > 0)
        ? vscode.ConfigurationTarget.Workspace
        : vscode.ConfigurationTarget.Global;
      await cfg.update('target', nextTriple, targetScope);
      if (client && client.ready) {
        try {
          await client.sendRequest('workspace/executeCommand', {
            command: 'lowlight.setTarget',
            arguments: [nextTriple],
          });
        } catch (err) {
          vscode.window.showErrorMessage(`Failed to switch Lowlight target: ${err && err.message ? err.message : String(err)}`);
          return;
        }
      }
      vscode.window.setStatusBarMessage(`Lowlight target: ${nextTriple || 'host default'}`, 4000);
    })
  );

  function getOrCreateRunTerminal(name) {
    if (!runTerminal || runTerminal.exitStatus !== undefined) {
      const stdRoot = resolveStdlibRoot(context);
      const folders = vscode.workspace.workspaceFolders || [];
      const cwd = folders.length > 0 ? folders[0].uri.fsPath : undefined;
      const env = {};
      if (stdRoot) {
        env.LOWLIGHT_ROOT = stdRoot;
      }
      runTerminal = vscode.window.createTerminal({ name, cwd, env });
    }
    return runTerminal;
  }

  function quoteShellArg(s) {
    const str = String(s);
    if (process.platform === 'win32') {
      return `"${str.replace(/"/g, '\\"')}"`;
    }
    return `'${str.replace(/'/g, "'\\''")}'`;
  }
}

/**
 * @returns {Promise<void>}
 */
async function deactivate() {
  if (client) {
    await client.stop();
    client = null;
  }
}

module.exports = {
  activate,
  deactivate,
};

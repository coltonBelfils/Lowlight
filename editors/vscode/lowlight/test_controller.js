'use strict';

const cp = require('child_process');
const path = require('path');

let vscode = null;
try {
  vscode = require('vscode');
} catch (_) {
  // Running pure helper tests outside VS Code extension host.
}

/**
 * Normalizes file paths for stable cross-platform TestItem IDs.
 * @param {string} p
 * @returns {string}
 */
function normalizeFsPath(p) {
  return String(p || '').replace(/\\/g, '/');
}

/**
 * Escapes regex metacharacters so `-run` matches an exact test symbol name.
 * @param {string} s
 * @returns {string}
 */
function escapeRegex(s) {
  return String(s || '').replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * Creates a fresh NDJSON stream parser state.
 */
function createNdjsonState() {
  return { buffer: '' };
}

/**
 * Splits incoming stdout chunks into complete lines and parsed JSON objects.
 * Non-JSON lines are returned in `rawLines` so diagnostics remain visible.
 *
 * @param {string | Buffer} chunk
 * @param {{ buffer: string }} state
 * @returns {{ events: any[], rawLines: string[] }}
 */
function parseNdjsonLines(chunk, state) {
  const text = Buffer.isBuffer(chunk) ? chunk.toString('utf8') : String(chunk || '');
  state.buffer += text;
  const events = [];
  const rawLines = [];

  let nlIdx;
  while ((nlIdx = state.buffer.indexOf('\n')) !== -1) {
    const line = state.buffer.slice(0, nlIdx).replace(/\r$/, '');
    state.buffer = state.buffer.slice(nlIdx + 1);
    if (!line.trim()) {
      continue;
    }
    if (line.trimStart().startsWith('{')) {
      try {
        events.push(JSON.parse(line));
        continue;
      } catch (_) {
        // Fall through to rawLines if malformed
      }
    }
    rawLines.push(line);
  }
  return { events, rawLines };
}

/**
 * Flushes any trailing non-newline-terminated content at stream EOF.
 * @param {{ buffer: string }} state
 * @returns {{ events: any[], rawLines: string[] }}
 */
function flushNdjsonState(state) {
  if (!state.buffer || !state.buffer.trim()) {
    state.buffer = '';
    return { events: [], rawLines: [] };
  }
  const rest = state.buffer + '\n';
  state.buffer = '';
  return parseNdjsonLines(rest, state);
}

/**
 * Fast zero-spawn scanner used for live unsaved `.ll` edits before save triggers `--list --json`.
 *
 * @param {string} text
 * @param {string} filePath
 * @returns {Array<{ event: string, file: string, line: number, col: number, name: string, desc: string, isTestMain: boolean }>}
 */
function scanQuickTests(text, filePath) {
  const lines = String(text || '').split(/\r?\n/);
  const normFile = normalizeFsPath(filePath);
  const found = [];

  let pendingTest = null;
  let inBlockComment = false;

  for (let i = 0; i < lines.length; i++) {
    let line = lines[i];

    // Track simple multi-line /* ... */ comments while stripping single-line // comments outside strings.
    let trimmed = line.trim();
    if (inBlockComment) {
      const endIdx = trimmed.indexOf('*/');
      if (endIdx === -1) {
        continue;
      }
      trimmed = trimmed.slice(endIdx + 2).trim();
      inBlockComment = false;
    }
    if (trimmed.startsWith('/*')) {
      const endIdx = trimmed.indexOf('*/', 2);
      if (endIdx === -1) {
        inBlockComment = true;
        continue;
      }
      trimmed = trimmed.slice(endIdx + 2).trim();
    }
    if (!trimmed || trimmed.startsWith('//')) {
      continue;
    }

    const attrMatch = trimmed.match(/^@test(?![A-Za-z0-9_])(?:\(\s*"([^"]*)"\s*\))?/);
    if (attrMatch) {
      pendingTest = {
        desc: attrMatch[1] || '',
        attrLine: i + 1,
      };
      // Check if `bind name =` is on the same line after `@test...`, ignoring trailing `// comment`.
      const afterAttr = trimmed
        .slice(attrMatch[0].length)
        .replace(/\s*\/\/.*$/, '')
        .trim();
      if (!afterAttr) {
        continue;
      }
      trimmed = afterAttr;
    }

    const bindMatch = trimmed.match(/^bind\s+([A-Za-z_][A-Za-z0-9_]*)\s*=/);
    if (bindMatch) {
      const fnName = bindMatch[1];
      const rawCol = Math.max(1, line.indexOf('bind') + 1);
      if (fnName === 'test_main') {
        found.push({
          event: 'test',
          file: normFile,
          line: i + 1,
          col: rawCol,
          name: fnName,
          desc: fnName,
          isTestMain: true,
        });
        pendingTest = null;
        continue;
      }
      if (pendingTest) {
        found.push({
          event: 'test',
          file: normFile,
          line: i + 1,
          col: rawCol,
          name: fnName,
          desc: pendingTest.desc || fnName,
          isTestMain: false,
        });
        pendingTest = null;
        continue;
      }
    }

    if (trimmed.startsWith('@')) {
      // Preserve pendingTest across stacked attributes like `@test` + `@test_only` or `@track_caller`.
      continue;
    }

    pendingTest = null;
  }

  return found;
}

/**
 * Converts a `fail` NDJSON event into structured message descriptors suitable for `vscode.TestMessage`.
 *
 * @param {any} ev
 * @returns {Array<{ isDiff: boolean, message: string, expected?: string, actual?: string, file?: string, line?: number, col?: number }>}
 */
function buildFailureDescriptors(ev) {
  if (!ev) {
    return [{ isDiff: false, message: 'test failed' }];
  }

  if (Array.isArray(ev.cases) && ev.cases.length > 0) {
    return ev.cases.map((c) => {
      const header = `case [${c.index}]${c.label ? ` (${c.label})` : ''}`;
      const body = c.message || 'case failed';
      const file = c.failFile || ev.failFile || ev.file;
      const line = c.failLine || ev.failLine || ev.line;
      const col = c.failCol || ev.failCol || ev.col;
      if (typeof c.expected === 'string' && typeof c.actual === 'string') {
        return {
          isDiff: true,
          message: `${header}: ${body}`,
          expected: c.expected,
          actual: c.actual,
          file,
          line,
          col,
        };
      }
      return {
        isDiff: false,
        message: `${header}\n${body}`,
        file,
        line,
        col,
      };
    });
  }

  const file = ev.failFile || ev.file;
  const line = ev.failLine || ev.line;
  const col = ev.failCol || ev.col;

  if (typeof ev.expected === 'string' && typeof ev.actual === 'string') {
    return [{
      isDiff: true,
      message: ev.message || 'values are not equal',
      expected: ev.expected,
      actual: ev.actual,
      file,
      line,
      col,
    }];
  }

  return [{
    isDiff: false,
    message: ev.message || 'test failed',
    file,
    line,
    col,
  }];
}

/**
 * Formats an NDJSON event for VS Code's Test Results terminal output (`\r\n` line endings).
 *
 * @param {any} ev
 * @returns {string}
 */
function formatEventForOutput(ev) {
  if (!ev || !ev.event) {
    return '';
  }
  switch (ev.event) {
    case 'run':
      return `=== RUN   ${ev.desc || ev.name}\r\n`;
    case 'pass': {
      const prefix = ev.output ? `${String(ev.output).replace(/\r?\n/g, '\r\n')}\r\n` : '';
      return `${prefix}--- PASS: ${ev.desc || ev.name} (${ev.durationUs || 0}µs, 0 leaks)\r\n`;
    }
    case 'fail': {
      const prefix = ev.output ? `${String(ev.output).replace(/\r?\n/g, '\r\n')}\r\n` : '';
      const loc = ev.failFile
        ? `${ev.relFile || path.basename(ev.failFile)}:${ev.failLine || 1}:${ev.failCol || 1}: `
        : '';
      const rawMsg = ev.message ? String(ev.message) : 'failed';
      const lines = rawMsg.split(/\r?\n/);
      let body;
      if (lines.length === 1 && !loc) {
        body = `--- FAIL: ${ev.desc || ev.name} (${lines[0]})\r\n`;
      } else {
        const renderedLines = lines.map((l) => (l.length > 0 ? `    ${l}` : '')).join('\r\n');
        body = `--- FAIL: ${ev.desc || ev.name}\r\n${renderedLines}\r\n`;
      }
      return `${prefix}${body}`;
    }
    case 'build_error':
      return `BUILD ERROR (${ev.package || ev.file || 'package'}):\r\n${String(ev.message || '').replace(/\r?\n/g, '\r\n')}\r\n`;
    case 'summary':
      return `\r\nTest results: ${ev.passed || 0} passed, ${ev.failed || 0} failed across ${ev.packages || 1} package(s) (total ${ev.total || 0})\r\n`;
    default:
      return '';
  }
}

/**
 * Instantiates and registers the native VS Code TestController for Lowlight.
 *
 * @param {import('vscode').ExtensionContext} context
 * @param {{ resolveServerBinary: (ctx: any) => string, resolveStdlibRoot: (ctx: any) => string, log?: (msg: string) => void }} deps
 */
function createLowlightTestController(context, deps) {
  if (!vscode || !vscode.tests || typeof vscode.tests.createTestController !== 'function') {
    return null;
  }

  const controller = vscode.tests.createTestController('lowlightTests', 'Lowlight Tests');
  const fileItemsByPath = new Map();
  const itemMetaById = new WeakMap();
  const editTimers = new Map();

  function log(msg) {
    if (deps && typeof deps.log === 'function') {
      deps.log(`[tests] ${msg}`);
    }
  }

  function fileItemId(absPath) {
    return `file:${normalizeFsPath(absPath)}`;
  }

  function testItemId(absPath, name) {
    return `test:${normalizeFsPath(absPath)}::${name}`;
  }

  function getEnvAndCwd(targetPath) {
    const stdRoot = deps.resolveStdlibRoot(context);
    const env = Object.assign({}, process.env);
    if (stdRoot) {
      env.LOWLIGHT_ROOT = stdRoot;
    }

    let cwd = '';
    if (targetPath) {
      const folder = vscode.workspace.getWorkspaceFolder(vscode.Uri.file(targetPath));
      if (folder) {
        cwd = folder.uri.fsPath;
      }
    }
    if (!cwd && vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders.length > 0) {
      cwd = vscode.workspace.workspaceFolders[0].uri.fsPath;
    }
    if (!cwd && targetPath) {
      cwd = path.dirname(targetPath);
    }
    return { env, cwd };
  }

  function displayLabelForFile(absPath) {
    const folder = vscode.workspace.getWorkspaceFolder(vscode.Uri.file(absPath));
    if (folder) {
      const rel = path.relative(folder.uri.fsPath, absPath);
      if (rel && !rel.startsWith('..')) {
        return normalizeFsPath(rel);
      }
    }
    return path.basename(absPath);
  }

  /**
   * Upserts a file TestItem and its child @test items cleanly.
   * Removes the file TestItem if `tests` is empty.
   *
   * @param {string} absPath
   * @param {Array<any>} tests
   */
  function applyFileTests(absPath, tests) {
    const normPath = normalizeFsPath(absPath);
    const fId = fileItemId(normPath);

    if (!tests || tests.length === 0) {
      if (fileItemsByPath.has(normPath)) {
        fileItemsByPath.delete(normPath);
        controller.items.delete(fId);
      }
      return null;
    }

    let fileItem = fileItemsByPath.get(normPath);
    if (!fileItem) {
      fileItem = controller.createTestItem(fId, displayLabelForFile(absPath), vscode.Uri.file(absPath));
      fileItemsByPath.set(normPath, fileItem);
      controller.items.add(fileItem);
    } else {
      fileItem.label = displayLabelForFile(absPath);
    }
    itemMetaById.set(fileItem, { kind: 'file', file: absPath });

    const seenIds = new Set();
    for (const t of tests) {
      const tId = testItemId(normPath, t.name);
      seenIds.add(tId);
      const lineIdx = Math.max(0, (t.line || 1) - 1);
      const colIdx = Math.max(0, (t.col || 1) - 1);
      const range = new vscode.Range(lineIdx, colIdx, lineIdx, colIdx);
      let child = fileItem.children.get(tId);
      if (!child) {
        child = controller.createTestItem(tId, t.desc || t.name, vscode.Uri.file(absPath));
        child.range = range;
        fileItem.children.add(child);
      } else {
        child.label = t.desc || t.name;
        child.range = range;
      }
      itemMetaById.set(child, {
        kind: 'test',
        file: absPath,
        name: t.name,
        desc: t.desc || t.name,
        isTestMain: Boolean(t.isTestMain),
      });
    }

    fileItem.children.forEach((existing) => {
      if (!seenIds.has(existing.id)) {
        fileItem.children.delete(existing.id);
      }
    });

    return fileItem;
  }

  /**
   * Executes `lowlight test --list --json <targets...>` to discover tests accurately.
   *
   * @param {string[]} targets
   * @returns {Promise<Array<any>>}
   */
  function listTestsCLI(targets) {
    return new Promise((resolve) => {
      if (!targets || targets.length === 0) {
        resolve([]);
        return;
      }
      const binPath = deps.resolveServerBinary(context);
      const { env, cwd } = getEnvAndCwd(targets[0]);
      const proc = cp.spawn(binPath, ['test', '--list', '--json', ...targets], {
        cwd: cwd || undefined,
        env,
        stdio: ['ignore', 'pipe', 'pipe'],
      });

      const state = createNdjsonState();
      const tests = [];

      proc.stdout.on('data', (chunk) => {
        const { events } = parseNdjsonLines(chunk, state);
        for (const ev of events) {
          if (ev && ev.event === 'test') {
            tests.push(ev);
          }
        }
      });

      proc.on('error', (err) => {
        log(`Failed to list tests: ${err && err.message ? err.message : String(err)}`);
        resolve(tests);
      });

      proc.on('close', () => {
        const { events } = flushNdjsonState(state);
        for (const ev of events) {
          if (ev && ev.event === 'test') {
            tests.push(ev);
          }
        }
        resolve(tests);
      });
    });
  }

  async function refreshFileTests(absPath) {
    const discovered = await listTestsCLI([absPath]);
    return applyFileTests(absPath, discovered);
  }

  async function discoverWorkspaceTests() {
    const folders = vscode.workspace.workspaceFolders || [];
    let targets = folders.map((f) => f.uri.fsPath);
    if (targets.length === 0) {
      targets = vscode.workspace.textDocuments
        .filter((d) => d.uri.scheme === 'file' && d.languageId === 'lowlight')
        .map((d) => d.uri.fsPath);
    }
    if (targets.length === 0) {
      return;
    }

    const discovered = await listTestsCLI(targets);
    const byFile = new Map();
    for (const tc of discovered) {
      const f = tc.file;
      if (!f) continue;
      const norm = normalizeFsPath(f);
      if (!byFile.has(norm)) {
        byFile.set(norm, { rawPath: f, items: [] });
      }
      byFile.get(norm).items.push(tc);
    }

    // Remove files that no longer have any tests.
    for (const [normPath] of fileItemsByPath.entries()) {
      if (!byFile.has(normPath)) {
        applyFileTests(normPath, []);
      }
    }
    for (const entry of byFile.values()) {
      applyFileTests(entry.rawPath, entry.items);
    }
  }

  function toVscodeLocation(file, line, col) {
    if (!file) {
      return undefined;
    }
    const ln = Math.max(0, (line || 1) - 1);
    const ch = Math.max(0, (col || 1) - 1);
    const pos = new vscode.Position(ln, ch);
    return new vscode.Location(vscode.Uri.file(file), new vscode.Range(pos, pos));
  }

  function toVscodeTestMessages(ev) {
    return buildFailureDescriptors(ev).map((desc) => {
      const msg = desc.isDiff
        ? vscode.TestMessage.diff(desc.message, desc.expected, desc.actual)
        : new vscode.TestMessage(desc.message);
      const loc = toVscodeLocation(desc.file, desc.line, desc.col);
      if (loc) {
        msg.location = loc;
      }
      return msg;
    });
  }

  /**
   * Collects all TestItems belonging to a file item (used for enqueuing and lookup).
   */
  function collectChildren(fileItem) {
    const items = [];
    fileItem.children.forEach((child) => items.push(child));
    return items;
  }

  function findTestItemForEvent(ev) {
    if (!ev) return null;
    if (ev.file) {
      const norm = normalizeFsPath(ev.file);
      const fItem = fileItemsByPath.get(norm);
      if (fItem) {
        if (ev.name) {
          const direct = fItem.children.get(testItemId(norm, ev.name));
          if (direct) return direct;
        }
        if (ev.desc) {
          let matched = null;
          fItem.children.forEach((child) => {
            const meta = itemMetaById.get(child);
            if (!matched && meta && (meta.desc === ev.desc || meta.name === ev.desc)) {
              matched = child;
            }
          });
          if (matched) return matched;
        }
      }
    }
    // Fallback lookup by description/name across all known file items.
    for (const fItem of fileItemsByPath.values()) {
      let matched = null;
      fItem.children.forEach((child) => {
        const meta = itemMetaById.get(child);
        if (!matched && meta && (meta.name === ev.name || meta.desc === ev.desc)) {
          matched = child;
        }
      });
      if (matched) return matched;
    }
    return null;
  }

  async function runSingleBatchInvocation(run, binPath, cliArgs, targetsToEnqueue, cwd, env, token) {
    for (const item of targetsToEnqueue) {
      run.enqueued(item);
    }

    return new Promise((resolve) => {
      if (token.isCancellationRequested) {
        resolve();
        return;
      }

      const proc = cp.spawn(binPath, cliArgs, {
        cwd: cwd || undefined,
        env,
        stdio: ['ignore', 'pipe', 'pipe'],
      });

      const cancelDisposable = token.onCancellationRequested(() => {
        try {
          proc.kill();
        } catch (_) {
          // Ignore kill errors if already exited
        }
      });

      const state = createNdjsonState();

      const handleEvent = (ev) => {
        const outText = formatEventForOutput(ev);
        const testItem = findTestItemForEvent(ev);
        if (outText) {
          const outLoc = (ev.event === 'fail' && ev.failFile)
            ? toVscodeLocation(ev.failFile, ev.failLine, ev.failCol)
            : (testItem && testItem.uri && testItem.range
              ? new vscode.Location(testItem.uri, testItem.range)
              : undefined);
          run.appendOutput(outText, outLoc, testItem || undefined);
        }

        switch (ev.event) {
          case 'run':
            if (testItem) {
              run.started(testItem);
            }
            break;
          case 'pass':
            if (testItem) {
              const ms = typeof ev.durationUs === 'number' ? ev.durationUs / 1000 : undefined;
              run.passed(testItem, ms);
            }
            break;
          case 'fail': {
            const target = testItem || targetsToEnqueue[0];
            if (target) {
              run.failed(target, toVscodeTestMessages(ev));
            }
            break;
          }
          case 'build_error': {
            const msg = new vscode.TestMessage(ev.message || 'Build failed');
            if (ev.file) {
              msg.location = toVscodeLocation(ev.file, 1, 1);
            }
            const fileNorm = ev.file ? normalizeFsPath(ev.file) : '';
            const matching = fileNorm && fileItemsByPath.get(fileNorm)
              ? collectChildren(fileItemsByPath.get(fileNorm))
              : targetsToEnqueue;
            for (const item of matching) {
              run.errored(item, msg);
            }
            break;
          }
        }
      };

      proc.stdout.on('data', (chunk) => {
        const { events, rawLines } = parseNdjsonLines(chunk, state);
        for (const raw of rawLines) {
          run.appendOutput(`${raw}\r\n`);
        }
        for (const ev of events) {
          handleEvent(ev);
        }
      });

      proc.stderr.on('data', (chunk) => {
        const text = Buffer.isBuffer(chunk) ? chunk.toString('utf8') : String(chunk);
        if (text) {
          run.appendOutput(text.replace(/\r?\n/g, '\r\n'));
        }
      });

      proc.on('error', (err) => {
        cancelDisposable.dispose();
        const errMsg = new vscode.TestMessage(`Failed to execute lowlight test runner: ${err.message}`);
        for (const item of targetsToEnqueue) {
          run.errored(item, errMsg);
        }
        resolve();
      });

      proc.on('close', () => {
        cancelDisposable.dispose();
        const { events, rawLines } = flushNdjsonState(state);
        for (const raw of rawLines) {
          run.appendOutput(`${raw}\r\n`);
        }
        for (const ev of events) {
          handleEvent(ev);
        }
        resolve();
      });
    });
  }

  async function executeRequest(request, token, releaseMode) {
    const run = controller.createTestRun(request);
    try {
      const binPath = deps.resolveServerBinary(context);
      const excludeSet = new Set(request.exclude || []);

      if (!request.include || request.include.length === 0) {
        if (fileItemsByPath.size === 0) {
          await discoverWorkspaceTests();
        }
        const allItems = [];
        for (const fItem of fileItemsByPath.values()) {
          for (const child of collectChildren(fItem)) {
            if (!excludeSet.has(child)) {
              allItems.push(child);
            }
          }
        }
        const folders = vscode.workspace.workspaceFolders || [];
        let targets = folders.map((f) => f.uri.fsPath);
        if (targets.length === 0) {
          targets = Array.from(fileItemsByPath.values())
            .map((fItem) => (fItem.uri ? fItem.uri.fsPath : ''))
            .filter(Boolean);
        }
        if (targets.length > 0) {
          const { env, cwd } = getEnvAndCwd(targets[0]);
          const args = ['test', '--json'];
          if (releaseMode) args.push('--release');
          args.push(...targets);
          await runSingleBatchInvocation(run, binPath, args, allItems, cwd, env, token);
        }
        return;
      }

      // Group requested items by file so multi-test selections compile once per file.
      const byFile = new Map();
      const wholeFiles = new Set();

      for (const item of request.include) {
        if (excludeSet.has(item)) continue;
        const meta = itemMetaById.get(item);
        if (meta && meta.kind === 'file') {
          wholeFiles.add(meta.file);
          for (const child of collectChildren(item)) {
            if (!excludeSet.has(child)) {
              if (!byFile.has(meta.file)) byFile.set(meta.file, []);
              byFile.get(meta.file).push(child);
            }
          }
        } else if (meta && meta.kind === 'test') {
          if (!byFile.has(meta.file)) byFile.set(meta.file, []);
          byFile.get(meta.file).push(item);
        } else if (item.uri) {
          const fPath = item.uri.fsPath;
          if (!byFile.has(fPath)) byFile.set(fPath, []);
          byFile.get(fPath).push(item);
        }
      }

      for (const [filePath, items] of byFile.entries()) {
        if (token.isCancellationRequested) break;
        const normPath = normalizeFsPath(filePath);
        const fItem = fileItemsByPath.get(normPath);
        const totalInFile = fItem ? fItem.children.size : 0;
        const uniqueItems = Array.from(new Set(items));
        if (uniqueItems.length === 0) continue;

        const { env, cwd } = getEnvAndCwd(filePath);
        const args = ['test', '--json'];
        if (releaseMode) args.push('--release');

        const runAllInFile = wholeFiles.has(filePath) && excludeSet.size === 0
          || (totalInFile > 0 && uniqueItems.length >= totalInFile && excludeSet.size === 0);

        if (!runAllInFile) {
          const names = uniqueItems
            .map((it) => {
              const m = itemMetaById.get(it);
              return m && m.name ? escapeRegex(m.name) : escapeRegex(it.label);
            })
            .filter(Boolean);
          const hasTestMain = uniqueItems.some((it) => {
            const m = itemMetaById.get(it);
            return m && m.isTestMain;
          });
          if (!hasTestMain && names.length > 0) {
            const filterExpr = names.length === 1 ? `^${names[0]}$` : `^(${names.join('|')})$`;
            args.push('-run', filterExpr);
          }
        }
        args.push(filePath);

        await runSingleBatchInvocation(run, binPath, args, uniqueItems, cwd, env, token);
      }
    } finally {
      run.end();
    }
  }

  function createProfileHandler(releaseMode) {
    return async (request, token) => {
      await executeRequest(request, token, releaseMode);

      if (request.continuous && !token.isCancellationRequested) {
        await new Promise((resolve) => {
          const watchedFiles = new Set();
          if (request.include && request.include.length > 0) {
            for (const item of request.include) {
              if (item.uri) {
                watchedFiles.add(normalizeFsPath(item.uri.fsPath));
              }
            }
          }
          let running = false;
          let queued = false;
          const trigger = async () => {
            if (token.isCancellationRequested) return;
            if (running) {
              queued = true;
              return;
            }
            running = true;
            try {
              await executeRequest(request, token, releaseMode);
            } finally {
              running = false;
              if (queued && !token.isCancellationRequested) {
                queued = false;
                trigger();
              }
            }
          };

          const saveSub = vscode.workspace.onDidSaveTextDocument(async (doc) => {
            if (doc.languageId !== 'lowlight' || doc.uri.scheme !== 'file') return;
            await refreshFileTests(doc.uri.fsPath);
            if (watchedFiles.size === 0 || watchedFiles.has(normalizeFsPath(doc.uri.fsPath))) {
              trigger();
            }
          });
          const cancelSub = token.onCancellationRequested(() => {
            saveSub.dispose();
            cancelSub.dispose();
            resolve();
          });
        });
      }
    };
  }

  controller.refreshHandler = async () => {
    await discoverWorkspaceTests();
  };

  controller.resolveHandler = async (item) => {
    if (!item) {
      await discoverWorkspaceTests();
    } else if (item.uri && item.uri.scheme === 'file') {
      await refreshFileTests(item.uri.fsPath);
    }
  };

  controller.createRunProfile(
    'Run',
    vscode.TestRunProfileKind.Run,
    createProfileHandler(false),
    true,
    undefined,
    true
  );

  controller.createRunProfile(
    'Run (Release)',
    vscode.TestRunProfileKind.Run,
    createProfileHandler(true),
    false,
    undefined,
    true
  );

  // Sync Test Explorer immediately on open/edit/save and FS changes.
  const llWatcher = vscode.workspace.createFileSystemWatcher('**/*.ll');
  context.subscriptions.push(
    controller,
    llWatcher,
    llWatcher.onDidCreate((uri) => refreshFileTests(uri.fsPath)),
    llWatcher.onDidChange((uri) => refreshFileTests(uri.fsPath)),
    llWatcher.onDidDelete((uri) => applyFileTests(uri.fsPath, [])),
    vscode.workspace.onDidOpenTextDocument((doc) => {
      if (doc.languageId === 'lowlight' && doc.uri.scheme === 'file') {
        const quick = scanQuickTests(doc.getText(), doc.uri.fsPath);
        if (quick.length > 0) {
          applyFileTests(doc.uri.fsPath, quick);
        }
        refreshFileTests(doc.uri.fsPath);
      }
    }),
    vscode.workspace.onDidSaveTextDocument((doc) => {
      if (doc.languageId === 'lowlight' && doc.uri.scheme === 'file') {
        refreshFileTests(doc.uri.fsPath);
      }
    }),
    vscode.workspace.onDidChangeTextDocument((ev) => {
      const doc = ev.document;
      if (doc.languageId !== 'lowlight' || doc.uri.scheme !== 'file') return;
      const key = doc.uri.toString();
      if (editTimers.has(key)) {
        clearTimeout(editTimers.get(key));
      }
      editTimers.set(
        key,
        setTimeout(() => {
          editTimers.delete(key);
          applyFileTests(doc.uri.fsPath, scanQuickTests(doc.getText(), doc.uri.fsPath));
        }, 200)
      );
    })
  );

  // Seed already-open documents immediately on activation.
  for (const doc of vscode.workspace.textDocuments) {
    if (doc.languageId === 'lowlight' && doc.uri.scheme === 'file') {
      const quick = scanQuickTests(doc.getText(), doc.uri.fsPath);
      if (quick.length > 0) {
        applyFileTests(doc.uri.fsPath, quick);
      }
    }
  }
  discoverWorkspaceTests();

  return {
    controller,
    async runTestByCommand(filePath, testName) {
      await refreshFileTests(filePath);
      const norm = normalizeFsPath(filePath);
      const fItem = fileItemsByPath.get(norm);
      if (!fItem) {
        vscode.window.showInformationMessage(`No Lowlight tests found in ${path.basename(filePath)}.`);
        return;
      }
      let targets = [fItem];
      if (testName) {
        const exact = fItem.children.get(testItemId(norm, testName));
        if (exact) {
          targets = [exact];
        } else {
          fItem.children.forEach((child) => {
            const meta = itemMetaById.get(child);
            if (meta && (meta.name === testName || meta.desc === testName)) {
              targets = [child];
            }
          });
        }
      }
      const cts = new vscode.CancellationTokenSource();
      await executeRequest(new vscode.TestRunRequest(targets), cts.token, false);
    },
    async runAllTestsByCommand(filePath) {
      if (filePath) {
        await refreshFileTests(filePath);
        const fItem = fileItemsByPath.get(normalizeFsPath(filePath));
        const cts = new vscode.CancellationTokenSource();
        if (fItem) {
          await executeRequest(new vscode.TestRunRequest([fItem]), cts.token, false);
          return;
        }
      }
      await discoverWorkspaceTests();
      const cts = new vscode.CancellationTokenSource();
      await executeRequest(new vscode.TestRunRequest(), cts.token, false);
    },
    async refresh() {
      await discoverWorkspaceTests();
    },
  };
}

module.exports = {
  normalizeFsPath,
  escapeRegex,
  createNdjsonState,
  parseNdjsonLines,
  flushNdjsonState,
  scanQuickTests,
  buildFailureDescriptors,
  formatEventForOutput,
  createLowlightTestController,
};

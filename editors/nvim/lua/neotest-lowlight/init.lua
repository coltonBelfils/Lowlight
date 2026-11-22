-- neotest-lowlight: Native neotest adapter for the Lowlight programming language.
-- Uses `lowlight test --list --json` for test discovery and `lowlight test --json`
-- for structured streaming results, failure locations, and assertion diffs.

local lib = nil
pcall(function()
  lib = require('neotest.lib')
end)

local M = { name = 'neotest-lowlight' }

local function regex_escape(s)
  return (tostring(s or ''):gsub('([%^%$%(%)%%%.%[%]%*%+%-%?])', '\\%1'))
end

local function find_root(dir)
  if lib and lib.files and lib.files.match_root_pattern then
    return lib.files.match_root_pattern('lowlight.toml', '.lowlightfmt', 'std', '.git', 'go.mod')(dir)
  end
  return vim.fs.root(dir, { 'lowlight.toml', '.lowlightfmt', 'std', '.git', 'go.mod' })
end

M.root = find_root

function M.filter_dir(name)
  return name ~= '.git' and name ~= 'node_modules' and name ~= 'editors' and name ~= 'target'
end

function M.is_test_file(file_path)
  return type(file_path) == 'string' and file_path:sub(-3) == '.ll'
end

--- Discovers `@test` and `test_main` positions via `lowlight test --list --json`.
function M.discover_positions(path)
  local Tree = require('neotest.types').Tree
  local lines = vim.fn.systemlist({ 'lowlight', 'test', '--list', '--json', path })
  local positions = {
    {
      id = path,
      name = vim.fn.fnamemodify(path, ':t'),
      path = path,
      type = 'file',
      range = { 0, 0, 0, 0 },
    },
  }

  for _, raw in ipairs(lines or {}) do
    local ok, ev = pcall(vim.json.decode, raw)
    if ok and type(ev) == 'table' and ev.event == 'test' then
      local ln = math.max(0, (ev.line or 1) - 1)
      local col = math.max(0, (ev.col or 1) - 1)
      table.insert(positions, {
        id = (ev.file or path) .. '::' .. (ev.name or 'test'),
        name = ev.desc or ev.name or 'test',
        path = ev.file or path,
        type = 'test',
        range = { ln, col, ln, col },
        is_test_main = ev.isTestMain or false,
        symbol_name = ev.name,
      })
    end
  end

  return Tree.from_list(positions, function(pos)
    return pos.id
  end)
end

---@param args table
function M.build_spec(args)
  local position = args.tree:data()
  local root_dir = find_root(position.path) or vim.fn.getcwd()
  local env = {}
  if vim.fn.isdirectory(root_dir .. '/std') == 1 then
    env.LOWLIGHT_ROOT = root_dir
  end

  local command = { 'lowlight', 'test', '--json' }
  if position.type == 'test' and not position.is_test_main then
    local sym = position.symbol_name or position.name
    table.insert(command, '-run')
    table.insert(command, '^' .. regex_escape(sym) .. '$')
  end
  table.insert(command, position.path)

  return {
    command = command,
    cwd = root_dir,
    env = env,
    context = {
      root = root_dir,
      target = position.path,
    },
  }
end

function M.results(spec, result, tree)
  local results = {}
  local raw = ''
  if result and result.output and vim.fn.filereadable(result.output) == 1 then
    raw = table.concat(vim.fn.readfile(result.output), '\n')
  end

  local by_sym = {}
  for _, node in tree:iter_nodes() do
    local data = node:data()
    if data.type == 'test' then
      by_sym[data.symbol_name or data.name] = data.id
      by_sym[data.name] = data.id
    end
  end

  for line in (raw .. '\n'):gmatch('(.-)\n') do
    if line:sub(1, 1) == '{' then
      local ok, ev = pcall(vim.json.decode, line)
      if ok and type(ev) == 'table' then
        local tid = by_sym[ev.name or ''] or by_sym[ev.desc or '']
          or ((ev.file or '') .. '::' .. (ev.name or ''))
        if ev.event == 'pass' then
          results[tid] = {
            status = 'passed',
            short = string.format('%s (%dµs)', ev.desc or ev.name or '', ev.durationUs or 0),
          }
        elseif ev.event == 'fail' then
          local fail_line = math.max(0, ((ev.failLine or ev.line or 1) - 1))
          results[tid] = {
            status = 'failed',
            short = (ev.message or 'failed'):match('^([^\n]+)') or 'failed',
            errors = {
              {
                line = fail_line,
                message = ev.message or 'Assertion failed',
              },
            },
          }
        elseif ev.event == 'build_error' then
          for _, id in pairs(by_sym) do
            results[id] = {
              status = 'failed',
              short = 'Build error',
              errors = {
                {
                  message = ev.message or 'Build error',
                },
              },
            }
          end
        end
      end
    end
  end

  return results
end

return M

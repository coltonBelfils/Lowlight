package format

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Options configures Lowlight code formatting.
type Options struct {
	// IndentStyle is "tabs" (canonical default) or "spaces".
	IndentStyle string `json:"indent_style"`
	// TabWidth is the display width of a tab stop and the space count when IndentStyle == "spaces".
	TabWidth int `json:"tab_width"`
	// LineWidth is the soft wrapping column limit (default 100; <= 0 disables line wrapping).
	LineWidth int `json:"line_width"`
}

// DefaultOptions returns canonical Lowlight formatting options.
func DefaultOptions() Options {
	return Options{
		IndentStyle: "tabs",
		TabWidth:    4,
		LineWidth:   100,
	}
}

// Normalized returns a copy of Options with valid defaults applied.
func (o Options) Normalized() Options {
	style := strings.ToLower(strings.TrimSpace(o.IndentStyle))
	switch style {
	case "spaces", "space":
		o.IndentStyle = "spaces"
	default:
		o.IndentStyle = "tabs"
	}
	if o.TabWidth <= 0 {
		o.TabWidth = 4
	}
	return o
}

// IndentUnit returns one level of indentation according to Options.
func (o Options) IndentUnit() string {
	n := o.Normalized()
	if n.IndentStyle == "spaces" {
		return strings.Repeat(" ", n.TabWidth)
	}
	return "\t"
}

// IndentString returns level copies of IndentUnit.
func (o Options) IndentString(level int) string {
	if level <= 0 {
		return ""
	}
	return strings.Repeat(o.IndentUnit(), level)
}

// VisualLen returns the display width of s on a single line, counting each '\t' as TabWidth columns.
func (o Options) VisualLen(s string) int {
	tw := o.TabWidth
	if tw <= 0 {
		tw = 4
	}
	cols := 0
	for _, r := range s {
		if r == '\t' {
			cols += tw
		} else {
			cols++
		}
	}
	return cols
}

// LoadConfig searches from dir upward for `.lowlightfmt` and returns the loaded Options
// (falling back to DefaultOptions if none is found or on parse failure).
func LoadConfig(dir string) Options {
	opts, _ := LoadConfigFound(dir)
	return opts
}

// LoadConfigFound searches from dir upward for `.lowlightfmt`, returning the loaded
// Options and whether a `.lowlightfmt` file was found.
func LoadConfigFound(dir string) (Options, bool) {
	opts := DefaultOptions()
	if dir == "" {
		return opts, false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		cfgPath := filepath.Join(abs, ".lowlightfmt")
		if data, err := os.ReadFile(cfgPath); err == nil {
			return parseConfigData(string(data), opts), true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return opts, false
}

func parseConfigData(raw string, base Options) Options {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return base
	}
	if strings.HasPrefix(trimmed, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(trimmed), &m); err == nil {
			for k, v := range m {
				applyConfigKV(&base, k, stringifyConfigVal(v))
			}
			return base.Normalized()
		}
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		sep := "="
		if !strings.Contains(line, "=") && strings.Contains(line, ":") {
			sep = ":"
		}
		idx := strings.Index(line, sep)
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if cIdx := strings.Index(v, "#"); cIdx >= 0 {
			v = strings.TrimSpace(v[:cIdx])
		}
		v = strings.Trim(v, `"'`)
		applyConfigKV(&base, k, v)
	}
	return base.Normalized()
}

func stringifyConfigVal(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.Itoa(int(t))
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

func applyConfigKV(opts *Options, key, val string) {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.ReplaceAll(k, "-", "_")
	switch k {
	case "indent_style", "indentstyle", "indent":
		opts.IndentStyle = val
	case "tab_width", "tabwidth", "indent_size", "indentsize":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			opts.TabWidth = n
		}
	case "line_width", "linewidth", "max_line_width", "print_width":
		if strings.EqualFold(val, "off") || strings.EqualFold(val, "none") || strings.EqualFold(val, "false") {
			opts.LineWidth = 0
		} else if n, err := strconv.Atoi(val); err == nil {
			if n < 0 {
				n = 0
			}
			opts.LineWidth = n
		}
	}
}

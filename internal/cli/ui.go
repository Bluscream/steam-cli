package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"golang.org/x/term"
)

// colorMode controls ANSI output. "auto" enables colour only when stdout is a
// terminal and NO_COLOR is unset, so redirected or piped output stays clean and
// remains safe to parse.
func (o *options) applyColor(out io.Writer) {
	switch o.color {
	case "always":
		text.EnableColors()
		return
	case "never":
		text.DisableColors()
		return
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		text.DisableColors()
		return
	}
	f, isFile := out.(*os.File)
	if isFile && term.IsTerminal(int(f.Fd())) {
		text.EnableColors()
		return
	}
	text.DisableColors()
}

// newTable returns a table bound to w, styled consistently across commands.
func (o *options) newTable(w io.Writer) table.Writer {
	if o.format == "csv" {
		text.DisableColors()
	} else {
		o.applyColor(w)
	}
	t := table.NewWriter()
	t.SetOutputMirror(w)

	s := table.StyleRounded
	s.Options.SeparateRows = false
	s.Format.Header = text.FormatUpper
	s.Color.Header = text.Colors{text.Bold}
	t.SetStyle(s)
	return t
}

// newDetail returns a two-column key/value table without a header row, for
// showing one object rather than a list.
func (o *options) newDetail(w io.Writer) table.Writer {
	t := o.newTable(w)
	t.Style().Options.SeparateHeader = false
	cfg := []table.ColumnConfig{{Number: 1, Colors: text.Colors{text.FgCyan}}}
	// A single long value — launch options, a file path, a description — would
	// otherwise stretch the table far past the terminal and wrap mid-cell in
	// the shell, which breaks the borders. Wrapping inside the cell keeps the
	// table intact. CSV is left alone: it exists to be parsed.
	if width := o.detailWidth(w); width > 0 {
		cfg = append(cfg, table.ColumnConfig{Number: 2, WidthMax: width})
	}
	t.SetColumnConfigs(cfg)
	return t
}

// detailWidth returns the room a detail table's value column has, or 0 when the
// output is not a terminal and should not be wrapped at all.
func (o *options) detailWidth(w io.Writer) int {
	if o.format == "csv" {
		return 0
	}
	cols := 0
	// An explicit COLUMNS wins, so output piped into a pager can still be
	// wrapped to the width the user actually has.
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		cols = n
	} else if f, ok := w.(*os.File); ok {
		if c, _, err := term.GetSize(int(f.Fd())); err == nil {
			cols = c
		}
	}
	if cols <= 0 {
		return 0
	}
	// Leave room for the label column and the box-drawing characters.
	const chrome = 28
	if cols-chrome < 20 {
		return 20
	}
	return cols - chrome
}

// renderTable renders the table to w according to the requested output format.
// If output format is "csv", RenderCSV is used; otherwise Render is used.
// If withHeader is false, header rows are omitted.
func (o *options) renderTable(t table.Writer) string {
	if !o.withHeader {
		t.ResetHeaders()
	}
	if o.format == "csv" {
		return t.RenderCSV()
	}
	return t.Render()
}

// detailRows appends only the pairs that have a value, so a detail view does
// not show rows the API did not return.
func detailRows(t table.Writer, pairs ...[2]string) {
	for _, p := range pairs {
		if strings.TrimSpace(p[1]) != "" {
			t.AppendRow(table.Row{p[0], p[1]})
		}
	}
}

func kv(k, v string) [2]string { return [2]string{k, v} }

// --- semantic colouring -----------------------------------------------------

var (
	green  = text.Colors{text.FgGreen}
	red    = text.Colors{text.FgRed}
	yellow = text.Colors{text.FgYellow}
	dim    = text.Colors{text.Faint}
)

// colorStatus renders a service or connection state in a colour matching its
// severity. The text is returned unchanged when colour is disabled.
func colorStatus(s string) string {
	switch strings.ToLower(s) {
	case "normal", "online", "ok", "running", "installed":
		return green.Sprint(strings.ToUpper(s))
	case "slow", "idle", "installing", "delayed":
		return yellow.Sprint(strings.ToUpper(s))
	case "down", "unreachable", "offline", "error", "failed", "suspended":
		return red.Sprint(strings.ToUpper(s))
	}
	return strings.ToUpper(s)
}

func colorBool(b bool) string {
	if b {
		return green.Sprint("yes")
	}
	return red.Sprint("no")
}

// colorOK marks success and failure in a batch result.
func colorOK(ok bool, okText, failText string) string {
	if ok {
		return green.Sprint(okText)
	}
	return red.Sprint(failText)
}

func faint(s string) string { return dim.Sprint(s) }

// thousandsT formats a numeric cell with group separators.
func thousandsT(v any) string {
	switch n := v.(type) {
	case int:
		return thousands(n)
	case int64:
		return thousands(int(n))
	}
	return fmt.Sprint(v)
}

// numberT groups digits for a human view and leaves them bare for CSV, which
// exists to be parsed. Quoting keeps "1,234,567" valid CSV, but a consumer
// should not have to strip separators out of a numeric column.
func (o *options) numberT() text.Transformer {
	if o.format == "csv" {
		return func(v any) string { return fmt.Sprint(v) }
	}
	return thousandsT
}

// sizeCell renders a byte count for the chosen output: human-readable for a
// table, bare bytes for CSV.
func (o *options) sizeCell(n int64) string {
	if o.format == "csv" {
		return strconv.FormatInt(n, 10)
	}
	return humanBytes(n)
}

// heading prints a section title above a table. go-pretty's own SetTitle wraps
// to the table's width, which breaks longer titles mid-word. In CSV mode,
// headings are suppressed to keep machine parsers clean.
func (o *options) heading(w io.Writer, format string, args ...any) {
	if o.format == "csv" {
		return
	}
	fmt.Fprintf(w, "\n%s\n", text.Colors{text.Bold}.Sprintf(format, args...))
}

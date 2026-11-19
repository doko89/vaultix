package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Output formats for --output.
const (
	formatTable = "table"
	formatJSON  = "json"
)

// writeJSON prints raw (a Manager response body) pretty-printed. Bodies that
// are not objects/arrays pass through verbatim.
func writeJSON(w io.Writer, raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		_, werr := fmt.Fprintf(w, "%s\n", strings.TrimSpace(string(raw)))
		return werr
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeTable prints headers plus rows as an aligned table.
func writeTable(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}

// writeKV prints Field/Value pairs (the shape for `show` commands).
func writeKV(w io.Writer, pairs [][2]string) {
	rows := make([][]string, 0, len(pairs))
	for _, p := range pairs {
		rows = append(rows, []string{p[0], p[1]})
	}
	writeTable(w, []string{"FIELD", "VALUE"}, rows)
}

// fmtTime renders an optional timestamp for tables ("-" when absent).
func fmtTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// fmtProgress renders an optional 0-100 progress value ("-" when absent).
func fmtProgress(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", *p)
}

// fmtBytes renders an optional byte counter ("-" when absent).
func fmtBytes(b *uint64) string {
	if b == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *b)
}

// fmtList joins values for a table cell ("-" when empty).
func fmtList(vs []string) string {
	if len(vs) == 0 {
		return "-"
	}
	return strings.Join(vs, ",")
}

// fmtBool renders enabled/disabled-style flags.
func fmtBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

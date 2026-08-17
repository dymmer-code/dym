package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

const filterFlagHelp = `Keep rows where field=value or field!=value (repeatable, AND-combined; ` +
	`field is the API's JSON field name, e.g. type, host, content.ip; ` +
	`for "destination" this matches if the value is one of the list's entries)`

const selectFlagHelp = `Comma-separated list of fields to show, in order (API JSON field names, e.g. id,type,content.ip)`

// addFilterAndSelectFlags registers --filter (repeatable) and --select on a
// list command, which supports narrowing rows (--filter) as well as
// projecting fields (--select).
func addFilterAndSelectFlags(cmd *cobra.Command, filterArgs *[]string, selectFields *string) {
	cmd.Flags().StringArrayVar(filterArgs, "filter", nil, filterFlagHelp)
	cmd.Flags().StringVar(selectFields, "select", "", selectFlagHelp)
}

// addSelectFlag registers --select on a single-object command (create,
// update, delete), which supports projecting fields but not filtering rows
// (there's only ever one row).
func addSelectFlag(cmd *cobra.Command, selectFields *string) {
	cmd.Flags().StringVar(selectFields, "select", "", selectFlagHelp)
}

// toRowMaps JSON round-trips v (a pointer or value that marshals to a JSON
// array of objects — e.g. a []api.Record, or a single-element slice wrapping
// one struct) into []map[string]any, so --filter/--select can work
// generically across api.Record, api.Mailbox, api.Forwarding, and
// api.SecretEntry without any per-type code. Callers rendering a single
// object (records create/update/delete) wrap it in a length-1 slice before
// calling this.
func toRowMaps(v any) ([]map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// applyFilters is the typed helper list commands use to keep the original
// typed slice and its row-map projection index-aligned: it JSON round-trips
// items into row maps, keeps only the items whose row map matches filters,
// and returns both the filtered typed slice (for the unchanged existing
// renderers when --select is unset) and the filtered row maps (for the
// generic --select renderers).
func applyFilters[T any](items []T, filters []fieldFilter) ([]T, []map[string]any, error) {
	rows, err := toRowMaps(items)
	if err != nil {
		return nil, nil, err
	}
	filteredItems := make([]T, 0, len(items))
	filteredRows := make([]map[string]any, 0, len(rows))
	for i, row := range rows {
		if matchesFilters(row, filters) {
			filteredItems = append(filteredItems, items[i])
			filteredRows = append(filteredRows, row)
		}
	}
	return filteredItems, filteredRows, nil
}

// fieldFilter is one parsed --filter operand: keep rows where the value at
// path equals (or, if negate, does not equal) value.
type fieldFilter struct {
	path   string
	value  string
	negate bool
}

// parseFilters parses repeated --filter flag values of the form
// "field=value" or "field!=value". Any value without an "=" at all is a
// user error.
func parseFilters(raw []string) ([]fieldFilter, error) {
	filters := make([]fieldFilter, 0, len(raw))
	for _, r := range raw {
		if idx := strings.Index(r, "!="); idx >= 0 {
			filters = append(filters, fieldFilter{path: r[:idx], value: r[idx+2:], negate: true})
			continue
		}
		if idx := strings.Index(r, "="); idx >= 0 {
			filters = append(filters, fieldFilter{path: r[:idx], value: r[idx+1:], negate: false})
			continue
		}
		return nil, fmt.Errorf("invalid --filter %q: expected field=value or field!=value", r)
	}
	return filters, nil
}

// getPath resolves a dot-separated path (e.g. "content.ip") against a row
// map, descending into nested maps one segment at a time. It reports false
// when any segment is missing or when an intermediate value isn't a map
// (e.g. the path expects to descend further than the data goes).
func getPath(row map[string]any, path string) (any, bool) {
	var cur any = row
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[part]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// matchesFilters reports whether row satisfies every filter (AND-combined).
// A path that doesn't resolve on this row means the row doesn't match that
// filter — no error, the row is just excluded. Values are compared via
// fmt.Sprint on both sides (equality-only, no type-aware comparison), except
// when the resolved value is a JSON array ([]any, e.g. "destination"): in
// that case the filter matches if any element's string form equals the
// operand (membership), not the whole array's string form.
func matchesFilters(row map[string]any, filters []fieldFilter) bool {
	for _, f := range filters {
		val, ok := getPath(row, f.path)
		if !ok {
			return false
		}
		matched := false
		if arr, isArr := val.([]any); isArr {
			for _, elem := range arr {
				if fmt.Sprint(elem) == f.value {
					matched = true
					break
				}
			}
		} else {
			matched = fmt.Sprint(val) == f.value
		}
		if f.negate {
			matched = !matched
		}
		if !matched {
			return false
		}
	}
	return true
}

// parseSelect splits a comma-separated --select value into trimmed,
// non-empty field names, in the given order. An empty raw string (the
// default, unset --select) returns nil, signaling callers to fall back to
// the existing fixed-column/full-object rendering untouched.
func parseSelect(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// writeSelectedTable renders rows as a table restricted to fields, in the
// given order, with uppercased field names (dots kept as-is, e.g.
// "content.ip" -> "CONTENT.IP") as column headers. A field that doesn't
// resolve on a given row renders as an empty cell. An empty rows slice
// prints emptyMsg instead of a header-only table.
func writeSelectedTable(w io.Writer, rows []map[string]any, fields []string, emptyMsg string) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, emptyMsg)
		return err
	}
	tw := newTabWriter(w)
	headers := make([]string, len(fields))
	for i, f := range fields {
		headers[i] = strings.ToUpper(f)
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		values := make([]string, len(fields))
		for i, f := range fields {
			if v, ok := getPath(row, f); ok && v != nil {
				values[i] = fmt.Sprint(v)
			}
		}
		fmt.Fprintln(tw, strings.Join(values, "\t"))
	}
	return tw.Flush()
}

// writeSelectedJSON renders rows restricted to fields, in the given order,
// building each row's JSON object by hand (rather than via a
// map[string]any + json.Marshal, which would sort keys alphabetically) so
// the requested field order is preserved in the output. Each field name is
// marshaled for correct key-quoting; each resolved value is marshaled
// individually, or rendered as JSON null when the field doesn't resolve on
// that row. When multiple is true the rows are wrapped in a JSON array
// (list commands); otherwise rows must contain exactly one row and a single
// JSON object is written (single-object commands).
func writeSelectedJSON(w io.Writer, rows []map[string]any, fields []string, multiple bool) error {
	buildObject := func(row map[string]any) (string, error) {
		var sb strings.Builder
		sb.WriteByte('{')
		for i, f := range fields {
			if i > 0 {
				sb.WriteByte(',')
			}
			keyJSON, err := json.Marshal(f)
			if err != nil {
				return "", err
			}
			sb.Write(keyJSON)
			sb.WriteByte(':')
			if v, ok := getPath(row, f); ok {
				valueJSON, err := json.Marshal(v)
				if err != nil {
					return "", err
				}
				sb.Write(valueJSON)
			} else {
				sb.WriteString("null")
			}
		}
		sb.WriteByte('}')
		return sb.String(), nil
	}

	if !multiple {
		if len(rows) != 1 {
			return fmt.Errorf("writeSelectedJSON: expected exactly one row, got %d", len(rows))
		}
		obj, err := buildObject(rows[0])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, obj)
		return err
	}

	var sb strings.Builder
	sb.WriteByte('[')
	for i, row := range rows {
		if i > 0 {
			sb.WriteByte(',')
		}
		obj, err := buildObject(row)
		if err != nil {
			return err
		}
		sb.WriteString(obj)
	}
	sb.WriteByte(']')
	_, err := fmt.Fprintln(w, sb.String())
	return err
}

// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

// ErrSkipRow reports a row the contract cannot represent. timestamp and log
// are required on PlatformLog, so a row missing either is dropped and named
// rather than served as a zero value the caller cannot account for.
var ErrSkipRow = errors.New("loganalytics: row is missing a required field")

// mapPlatformRows maps the records statement of a platform-logs response.
// Malformed rows are returned as errors alongside the good ones so the caller
// can log them without failing the whole query.
func mapPlatformRows(t azlogs.Table) ([]PlatformLogEntry, []error, error) {
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, nil, err
	}

	out := make([]PlatformLogEntry, 0, len(t.Rows))
	var skipped []error
	for i, row := range t.Rows {
		ts := rowTime(row, idx, "TimeGenerated")
		if ts.IsZero() {
			skipped = append(skipped, fmt.Errorf("%w: row %d has no parseable TimeGenerated", ErrSkipRow, i))
			continue
		}
		msg := rowString(row, idx, "LogMessage")
		if msg == "" {
			skipped = append(skipped, fmt.Errorf("%w: row %d has no LogMessage", ErrSkipRow, i))
			continue
		}
		out = append(out, PlatformLogEntry{
			Timestamp:       ts,
			LogMessage:      msg,
			Level:           rowString(row, idx, "Level"),
			ClusterInstance: rowString(row, idx, "ClusterInstance"),
			PodNamespace:    rowString(row, idx, "PodNamespace"),
			PodName:         rowString(row, idx, "PodName"),
			ContainerName:   rowString(row, idx, "ContainerName"),
			NodeName:        rowString(row, idx, "NodeName"),
			ContainerImage:  rowString(row, idx, "ContainerImage"),
			Labels:          parsePodLabels(rowString(row, idx, "Labels")),
		})
	}
	return out, skipped, nil
}

// parsePodLabels decodes the podLabels blob.
// Non-string values are rendered rather than dropped - a label value is
// always a string on a real pod, and showing an odd one beats hiding it.
func parsePodLabels(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var anyMap map[string]any
	if err := json.Unmarshal([]byte(raw), &anyMap); err != nil || len(anyMap) == 0 {
		return nil
	}
	out := make(map[string]string, len(anyMap))
	for k, v := range anyMap {
		switch s := v.(type) {
		case string:
			out[k] = s
		case nil:
			out[k] = ""
		default:
			out[k] = fmt.Sprintf("%v", s)
		}
	}
	return out
}

// mapPlatformFilterValues maps the values statement of a filter-values response.
func mapPlatformFilterValues(t azlogs.Table) ([]PlatformLogFilterValue, error) {
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, err
	}
	out := make([]PlatformLogFilterValue, 0, len(t.Rows))
	for _, row := range t.Rows {
		v := rowString(row, idx, "Value")
		if v == "" {
			continue
		}
		out = append(out, PlatformLogFilterValue{Value: v, Count: rowInt64(row, idx, "Count")})
	}
	return out, nil
}

// findScalarTable returns the single-cell value of the first table carrying a
// column of the given name.
//
// The query sends two tabular statements and Log Analytics answers with a
// table per statement. Selecting by column name rather than by position means
// the mapping does not rest on the service preserving statement order.
func findScalarTable(tables []azlogs.Table, column string) (int64, bool) {
	for _, t := range tables {
		idx, err := buildColumnIndex(t.Columns)
		if err != nil {
			continue
		}
		if _, ok := idx[column]; !ok || len(t.Rows) == 0 {
			continue
		}
		return rowInt64(t.Rows[0], idx, column), true
	}
	return 0, false
}

// findTable returns the first table carrying a column of the given name.
func findTable(tables []azlogs.Table, column string) (azlogs.Table, bool) {
	for _, t := range tables {
		idx, err := buildColumnIndex(t.Columns)
		if err != nil {
			continue
		}
		if _, ok := idx[column]; ok {
			return t, true
		}
	}
	return azlogs.Table{}, false
}

// rowInt64 reads a numeric cell. azlogs decodes JSON numbers as float64.
func rowInt64(row azlogs.Row, idx map[string]int, name string) int64 {
	i, ok := idx[name]
	if !ok || i >= len(row) {
		return 0
	}
	switch v := row[i].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

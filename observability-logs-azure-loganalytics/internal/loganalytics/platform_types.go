// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import "time"

// PlatformLogsParams captures a platform-log query before it is rendered to KQL.
// Multi-value fields OR within a field and AND with each other. An absent or
// empty slice is not a filter - it must not narrow the query to nothing.
type PlatformLogsParams struct {
	ClusterInstances []string
	Namespaces       []string
	PodNames         []string
	ContainerNames   []string

	// Labels are pod labels every returned record must carry, ANDed.
	// Keys must be validated with IsValidLabelKey before they get here.
	Labels map[string]string

	StartTime time.Time
	EndTime   time.Time

	Limit        int
	SortOrder    SortOrder
	SearchPhrase string
	LogLevels    []string
}

// PlatformLogEntry is the projected row shape for a platform-log query.
//
// PodIp has no counterpart: ContainerLogV2 carries no pod-IP column and
// KubernetesMetadata does not include one, so the field is left unset. It is
// optional in the adapter contract.
type PlatformLogEntry struct {
	Timestamp       time.Time
	LogMessage      string
	Level           string
	ClusterInstance string
	PodNamespace    string
	PodName         string
	ContainerName   string
	NodeName        string
	ContainerImage  string
	Labels          map[string]string
}

// PlatformLogsResult is what the client returns to the handler.
type PlatformLogsResult struct {
	Logs       []PlatformLogEntry
	TotalCount int
	TookMs     int
}

// PlatformLogFilter names a filter whose distinct values can be listed. It
// mirrors the request field that accepts that filter.
type PlatformLogFilter string

const (
	FilterClusterInstance PlatformLogFilter = "clusterInstance"
	FilterNamespace       PlatformLogFilter = "namespace"
	FilterPodName         PlatformLogFilter = "podName"
	FilterContainerName   PlatformLogFilter = "containerName"
)

// IsListableFilter reports whether a filter can have its values listed.
//
// Derived from filterColumnExpr rather than repeating its cases: a filter that
// is listable but has no column would render `| extend _value = ` and fail in
// Azure as invalid KQL, instead of being refused here with a 400.
func IsListableFilter(f PlatformLogFilter) bool {
	return filterColumnExpr(f) != ""
}

// PlatformLogFilterValuesParams asks for the distinct values one filter takes
// under Query. Query's selections for Filter itself are ignored - a filter
// that counted its own selection would offer only what is already picked.
type PlatformLogFilterValuesParams struct {
	Query       PlatformLogsParams
	Filter      PlatformLogFilter
	ValueSearch string
	MaxValues   int
}

// PlatformLogFilterValue is one value a filter takes, with how many records
// carry it.
type PlatformLogFilterValue struct {
	Value string
	Count int64
}

// PlatformLogFilterValuesResult is the filter-values equivalent of
// PlatformLogsResult.
type PlatformLogFilterValuesResult struct {
	Filter      PlatformLogFilter
	Values      []PlatformLogFilterValue
	TotalValues int64
	TookMs      int
}

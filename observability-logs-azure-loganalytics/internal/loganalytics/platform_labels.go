// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"regexp"
	"sort"
	"strings"
)

// labelKeyPattern is the Kubernetes label key grammar: an optional DNS
// subdomain prefix followed by "/", then a name of alphanumerics, "-", "_"
// and "." bounded by alphanumerics.
var labelKeyPattern = regexp.MustCompile(
	`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?` +
		`[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)

// IsValidLabelKey reports whether key is a well-formed Kubernetes label key.
// A malformed key must be rejected rather than dropped.
func IsValidLabelKey(key string) bool {
	if key == "" {
		return false
	}
	prefix, name := "", key
	if i := strings.Index(key, "/"); i >= 0 {
		prefix, name = key[:i], key[i+1:]
		if prefix == "" || len(prefix) > 253 {
			return false
		}
	}
	if name == "" || len(name) > 63 {
		return false
	}
	return labelKeyPattern.MatchString(key)
}

// FirstInvalidLabelKey returns the first key in labels that is not a valid
// Kubernetes label key.
func FirstInvalidLabelKey(labels map[string]string) (string, bool) {
	keys := sortedKeys(labels)
	for _, k := range keys {
		if !IsValidLabelKey(k) {
			return k, false
		}
	}
	return "", true
}

// sortedKeys returns the map's keys in a stable order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

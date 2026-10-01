// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"strings"
	"testing"
)

func TestIsValidLabelKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"openchoreo.dev/plane", true},
		{"openchoreo.dev/plane-id", true},
		{"app.kubernetes.io/name", true},
		{"simple", true},
		{"with_underscore", true},
		{"a", true},
		{"", false},
		{"/name", false},
		{"pre/fix/name", false},
		{"-leading", false},
		{"trailing-", false},
		{"UPPER.io/name", false},
		{"has space", false},
		// The reason this check exists: a key becomes an index expression,
		// so a key carrying query syntax must never reach the builder.
		{`x"] == "" or true or tostring(parse_json("{}")["y`, false},
		{strings.Repeat("a", 64), false},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 254) + "/name", false},
	}

	for _, c := range cases {
		if got := IsValidLabelKey(c.key); got != c.want {
			t.Errorf("IsValidLabelKey(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

func TestFirstInvalidLabelKey(t *testing.T) {
	if _, ok := FirstInvalidLabelKey(map[string]string{
		"openchoreo.dev/plane":   "controlplane",
		"app.kubernetes.io/name": "observer",
	}); !ok {
		t.Error("valid labels were reported invalid")
	}

	key, ok := FirstInvalidLabelKey(map[string]string{
		"openchoreo.dev/plane": "controlplane",
		"has space":            "x",
	})
	if ok || key != "has space" {
		t.Errorf("got (%q, %v), want (\"has space\", false)", key, ok)
	}
}

// The named key must be stable, or the same request reports a different key
// from one call to the next.
func TestFirstInvalidLabelKey_IsDeterministic(t *testing.T) {
	labels := map[string]string{"zz bad": "1", "aa bad": "2", "mm bad": "3"}
	for i := 0; i < 20; i++ {
		if key, _ := FirstInvalidLabelKey(labels); key != "aa bad" {
			t.Fatalf("got %q, want the lowest-sorting invalid key", key)
		}
	}
}

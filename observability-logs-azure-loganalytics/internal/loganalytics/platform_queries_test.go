// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

func basePlatformParams() PlatformLogsParams {
	return PlatformLogsParams{
		StartTime: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Limit:     100,
		SortOrder: SortDesc,
	}
}

func TestBuildPlatformLogsKQL_AllFilters(t *testing.T) {
	p := basePlatformParams()
	p.ClusterInstances = []string{"aks-a", "aks-b"}
	p.Namespaces = []string{"openchoreo-control-plane"}
	p.PodNames = []string{"controller-manager-0"}
	p.ContainerNames = []string{"manager"}
	p.Labels = map[string]string{"openchoreo.dev/plane": "controlplane"}
	p.SearchPhrase = "reconcile"
	p.LogLevels = []string{"ERROR"}

	got := BuildPlatformLogsKQL(p)

	for _, want := range []string{
		`| where PodNamespace in ("openchoreo-control-plane")`,
		`| where PodName in ("controller-manager-0")`,
		`| where ContainerName in ("manager")`,
		`| where ClusterInstance in~ ("aks-a", "aks-b")`,
		`| where tostring(LogMessage) contains "reconcile"`,
		`| where Level in ("ERROR")`,
		`| take 100`,
		`| order by TimeGenerated desc`,
		`| summarize Total = count()`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("query is missing %q\n%s", want, got)
		}
	}
}

// A label key must reach the query exactly as Kubernetes spells it.
func TestBuildPlatformLogsKQL_LabelKeysAreVerbatim(t *testing.T) {
	p := basePlatformParams()
	p.Labels = map[string]string{"openchoreo.dev/plane-id": "dp-1"}

	got := BuildPlatformLogsKQL(p)

	want := `tostring(parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/plane-id"]) == "dp-1"`
	if !strings.Contains(got, want) {
		t.Errorf("query is missing the verbatim label predicate %q\n%s", want, got)
	}
	if strings.Contains(got, "openchoreo_dev") {
		t.Error("label key was mangled; Log Analytics stores keys verbatim")
	}
}

// Go randomises map iteration, so without sorting the same filters would
// render a different query on every call.
func TestBuildPlatformLogsKQL_LabelOrderIsDeterministic(t *testing.T) {
	p := basePlatformParams()
	p.Labels = map[string]string{
		"openchoreo.dev/plane":    "dataplane",
		"openchoreo.dev/plane-id": "dp-1",
		"app.kubernetes.io/name":  "gateway",
	}

	first := BuildPlatformLogsKQL(p)
	for i := 0; i < 20; i++ {
		if got := BuildPlatformLogsKQL(p); got != first {
			t.Fatalf("query is not stable across calls\n%s\n---\n%s", first, got)
		}
	}
}

// An absent or empty multi-value filter means "not a filter", not "match
// nothing". Getting this backwards makes an unfiltered platform query return
// nothing at all, which looks like an empty cluster.
func TestBuildPlatformLogsKQL_EmptyFiltersAreNotFilters(t *testing.T) {
	p := basePlatformParams()
	p.ClusterInstances = []string{}
	p.Namespaces = nil
	p.PodNames = []string{}
	p.ContainerNames = nil
	p.Labels = map[string]string{}
	p.LogLevels = []string{}

	got := BuildPlatformLogsKQL(p)

	for _, unwanted := range []string{
		"| where PodNamespace in",
		"| where PodName in",
		"| where ContainerName in",
		"| where ClusterInstance in~",
		"| where Level in",
		"| where tostring(parse_json(tostring(KubernetesMetadata.podLabels))",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("empty filter produced a predicate %q\n%s", unwanted, got)
		}
	}
}

func TestBuildPlatformLogsKQL_Defaults(t *testing.T) {
	p := basePlatformParams()
	p.SortOrder = ""

	got := BuildPlatformLogsKQL(p)
	if !strings.Contains(got, "| order by TimeGenerated desc") {
		t.Errorf("expected desc by default\n%s", got)
	}
}

func TestBuildPlatformLogsKQL_EscapesValues(t *testing.T) {
	p := basePlatformParams()
	p.SearchPhrase = `he said "hi"` + "\n" + `and \ left`
	p.Namespaces = []string{`a" or true or "b`}

	got := BuildPlatformLogsKQL(p)

	if strings.Contains(got, "\n| where PodNamespace in (\"a\" or true") {
		t.Errorf("value broke out of its literal\n%s", got)
	}
	if !strings.Contains(got, `\"hi\"`) {
		t.Errorf("quotes were not escaped\n%s", got)
	}
}

// The filter being listed must not constrain its own values: a filter that
// counted its own selection would only ever offer what is already picked.
func TestBuildPlatformLogFilterValuesKQL_DropsOwnSelections(t *testing.T) {
	q := basePlatformParams()
	q.Namespaces = []string{"openchoreo-control-plane"}
	q.PodNames = []string{"controller-manager-0"}

	got := BuildPlatformLogFilterValuesKQL(PlatformLogFilterValuesParams{
		Query: q, Filter: FilterNamespace, MaxValues: 50,
	})

	if strings.Contains(got, "| where PodNamespace in (") {
		t.Errorf("namespace selections should be cleared when listing namespaces\n%s", got)
	}
	if !strings.Contains(got, `| where PodName in ("controller-manager-0")`) {
		t.Errorf("other filters should still narrow the values\n%s", got)
	}
	if !strings.Contains(got, "| extend _value = PodNamespace") {
		t.Errorf("values should be drawn from PodNamespace\n%s", got)
	}
	if !strings.Contains(got, "| take 50") {
		t.Errorf("maxValues should bound the list\n%s", got)
	}
}

func TestBuildPlatformLogFilterValuesKQL_ClusterInstanceIsDerived(t *testing.T) {
	got := BuildPlatformLogFilterValuesKQL(PlatformLogFilterValuesParams{
		Query: basePlatformParams(), Filter: FilterClusterInstance, MaxValues: 10,
	})
	// Base derives the column once; the values query reads it rather than
	// recomputing the expression, so the two cannot drift.
	if !strings.Contains(got, "| extend ClusterInstance = "+ClusterInstanceExpr) {
		t.Errorf("Base should derive ClusterInstance from _ResourceId\n%s", got)
	}
	if !strings.Contains(got, "| extend _value = ClusterInstance") {
		t.Errorf("values should read the derived column\n%s", got)
	}
	if strings.Count(got, ClusterInstanceExpr) != 1 {
		t.Errorf("the derivation should appear exactly once\n%s", got)
	}
}

// Records where the field is absent must not surface as an empty-string
// value: no filter value would select one.
func TestBuildPlatformLogFilterValuesKQL_DropsAbsentValues(t *testing.T) {
	got := BuildPlatformLogFilterValuesKQL(PlatformLogFilterValuesParams{
		Query: basePlatformParams(), Filter: FilterPodName, MaxValues: 10,
	})
	if !strings.Contains(got, "| where isnotempty(_value)") {
		t.Errorf("absent values should be dropped\n%s", got)
	}
}

// A value search is a literal substring match, never a pattern the caller
// supplies.
func TestBuildPlatformLogFilterValuesKQL_ValueSearchIsLiteral(t *testing.T) {
	got := BuildPlatformLogFilterValuesKQL(PlatformLogFilterValuesParams{
		Query: basePlatformParams(), Filter: FilterPodName,
		ValueSearch: `x" or true or "y`, MaxValues: 10,
	})
	if !strings.Contains(got, `| where _value contains "x\" or true or \"y"`) {
		t.Errorf("value search was not quoted as a literal\n%s", got)
	}
}

// --- the level ladder must agree with the classifier ---

var (
	keywordPairRe = regexp.MustCompile(`_msg contains "([^"]*)", "([^"]*)"`)
	aliasEqRe     = regexp.MustCompile(`_lvlUp == "([^"]*)", "([^"]*)"`)
	aliasInRe     = regexp.MustCompile(`_lvlUp in \(([^)]*)\), "([^"]*)"`)
)

// evalKeywordLadder interprets the generated KQL's keyword ladder the way
// Kusto would: first `contains` (case-insensitive substring) wins, otherwise
// the trailing default.
func evalKeywordLadder(kql, msg string) string {
	upper := strings.ToUpper(msg)
	for _, m := range keywordPairRe.FindAllStringSubmatch(kql, -1) {
		if strings.Contains(upper, strings.ToUpper(m[1])) {
			return m[2]
		}
	}
	// The default is the last literal before the closing paren of the case().
	trailing := regexp.MustCompile(`"DEBUG", "DEBUG", "([^"]*)"\)`).FindStringSubmatch(kql)
	if trailing == nil {
		return ""
	}
	return trailing[1]
}

// TestLevelExprMatchesClassifier pins the generated KQL against the Go
// classifier that labels the response. This is the coherence the OpenObserve
// module had to fix and the Azure component path still gets wrong: if the
// filter and the reported level disagree, an ERROR filter returns records
// shown as INFO, and the query is valid so nothing surfaces the mistake.
func TestLevelExprMatchesClassifier(t *testing.T) {
	kql := levelExpr()

	messages := []string{
		"an ERROR occurred",
		"INFO: retrying after ERROR",
		"warning: disk almost full",
		"WARN throttled",
		"debug trace follows",
		"a FATAL condition",
		"SEVERE fault",
		"nothing of note here",
		"",
		"Informational message",
	}

	for _, msg := range messages {
		want := extractLogLevel(msg)
		if got := evalKeywordLadder(kql, msg); got != want {
			t.Errorf("message %q: KQL ladder says %q, extractLogLevel says %q", msg, got, want)
		}
	}
}

// TestLevelAliasExprMatchesNormalizer does the same for the envelope path:
// a level read out of a structured message is normalised identically.
func TestLevelAliasExprMatchesNormalizer(t *testing.T) {
	kql := levelAliasExpr()

	mapping := map[string]string{}
	for _, m := range aliasEqRe.FindAllStringSubmatch(kql, -1) {
		mapping[m[1]] = m[2]
	}
	for _, m := range aliasInRe.FindAllStringSubmatch(kql, -1) {
		for _, raw := range strings.Split(m[1], ",") {
			mapping[strings.Trim(strings.TrimSpace(raw), `"`)] = m[2]
		}
	}

	for _, in := range []string{
		"warning", "WARNING", "information", "INFORMATIONAL",
		"critical", "fatal", "severe", "trace", "error", "info", "debug",
	} {
		up := strings.ToUpper(in)
		want := normalizeLevel(in)
		got, ok := mapping[up]
		if !ok {
			got = up // the KQL ladder's fall-through is _lvlUp
		}
		if got != want {
			t.Errorf("level %q: KQL alias ladder says %q, normalizeLevel says %q", in, got, want)
		}
	}
}

// Every envelope key the classifier checks must also be checked in KQL, or a
// structured message's own level would be read on one path and not the other.
func TestLevelExprCoversEveryEnvelopeKey(t *testing.T) {
	kql := levelExpr()
	for _, key := range levelEnvelopeKeys {
		if !strings.Contains(kql, "_envelope."+key) {
			t.Errorf("envelope key %q is checked in Go but not in KQL", key)
		}
	}
	// The envelope is read through parse_json so a message stored as a string
	// holding JSON text resolves the same way resolveLogLevel resolves it.
	if !strings.Contains(kql, "| extend _envelope = parse_json(_msg)") {
		t.Errorf("envelope should be parsed from the message text\n%s", kql)
	}
}

// AMA splits a container image into repo, name and tag, and `image` alone is
// the bare name - so the full reference has to be reassembled.
func TestBuildPlatformLogsKQL_ContainerImageIsReassembled(t *testing.T) {
	got := BuildPlatformLogsKQL(basePlatformParams())

	for _, want := range []string{
		"KubernetesMetadata.imageRepo",
		"KubernetesMetadata.image)",
		"KubernetesMetadata.imageTag",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("container image is missing %q\n%s", want, got)
		}
	}
	if !strings.Contains(got, "ContainerImage = strcat(") {
		t.Errorf("container image should be assembled with strcat\n%s", got)
	}
}

// The level ladder is expensive, so it only belongs in Base when a level
// filter has to run before the take.
func TestBuildPlatformLogsKQL_LevelLadderRunsWhereNeeded(t *testing.T) {
	marker := "| extend _lvlRaw = case("

	noFilter := BuildPlatformLogsKQL(basePlatformParams())
	base, page, found := strings.Cut(noFilter, ";\nBase")
	if !found {
		t.Fatalf("expected a let-bound Base\n%s", noFilter)
	}
	if strings.Contains(base, marker) {
		t.Errorf("without a level filter the ladder should not run over the window\n%s", base)
	}
	if !strings.Contains(page, marker) {
		t.Errorf("the ladder should still run over the page, since Level is projected\n%s", page)
	}

	p := basePlatformParams()
	p.LogLevels = []string{"ERROR"}
	withFilter := BuildPlatformLogsKQL(p)
	filterBase, _, _ := strings.Cut(withFilter, ";\nBase")
	if !strings.Contains(filterBase, marker) {
		t.Errorf("a level filter needs the ladder before the take\n%s", filterBase)
	}
	if strings.Count(withFilter, marker) != 1 {
		t.Errorf("the ladder should not be emitted twice\n%s", withFilter)
	}
}

// A filter-values query never projects Level, so it should not pay for the
// ladder unless it is filtering on one.
func TestBuildPlatformLogFilterValuesKQL_SkipsLevelLadder(t *testing.T) {
	got := BuildPlatformLogFilterValuesKQL(PlatformLogFilterValuesParams{
		Query: basePlatformParams(), Filter: FilterNamespace, MaxValues: 10,
	})
	if strings.Contains(got, "| extend _lvlRaw = case(") {
		t.Errorf("filter values should not compute the level\n%s", got)
	}
}

// The builder is exported, so it cannot rely on the handler having clamped.
func TestBuildPlatformLogFilterValuesKQL_ClampsMaxValues(t *testing.T) {
	zero := BuildPlatformLogFilterValuesKQL(PlatformLogFilterValuesParams{
		Query: basePlatformParams(), Filter: FilterNamespace, MaxValues: 0,
	})
	if !strings.Contains(zero, "| take 100") {
		t.Errorf("maxValues 0 should fall back to the default, not take 0\n%s", zero)
	}

	huge := BuildPlatformLogFilterValuesKQL(PlatformLogFilterValuesParams{
		Query: basePlatformParams(), Filter: FilterNamespace, MaxValues: 999999,
	})
	if !strings.Contains(huge, "| take 1000") {
		t.Errorf("maxValues should be capped\n%s", huge)
	}
}

// FATAL and SEVERE are outside the contract's filter enum, so they fold into
// ERROR - otherwise logLevels=["ERROR"] would miss them and the same message
// would classify differently depending on whether it arrived structured.
func TestFatalAndSevereFoldIntoError(t *testing.T) {
	for _, msg := range []string{"a FATAL condition", "SEVERE fault"} {
		if got := extractLogLevel(msg); got != "ERROR" {
			t.Errorf("extractLogLevel(%q) = %q, want ERROR", msg, got)
		}
		if got := evalKeywordLadder(levelExpr(), msg); got != "ERROR" {
			t.Errorf("KQL ladder for %q = %q, want ERROR", msg, got)
		}
	}
	if got := normalizeLevel("fatal"); got != "ERROR" {
		t.Errorf("envelope path disagrees: normalizeLevel(fatal) = %q", got)
	}
}

var envelopeKeyRe = regexp.MustCompile(`gettype\(_envelope\.([A-Za-z_]+)\) == "string"`)

// evalLevelExpr interprets the whole generated ladder the way Kusto would:
// the envelope lookup, then alias normalisation, then the keyword scan.
//
// The ladder-specific tests each check one stage in isolation, which is why
// they could not see a divergence that lived in how the stages were reached.
func evalLevelExpr(kql, msg string) string {
	// Stage 1: the envelope, read through parse_json as the KQL does - no
	// leading-brace guard.
	raw := ""
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(msg), &envelope); err == nil {
		for _, m := range envelopeKeyRe.FindAllStringSubmatch(kql, -1) {
			v, ok := envelope[m[1]]
			if !ok {
				continue
			}
			var str string
			// gettype(...) == "string" in KQL, a string unmarshal here.
			if err := json.Unmarshal(v, &str); err != nil || str == "" {
				continue
			}
			raw = str
			break
		}
	}

	// Stage 2: alias normalisation of an envelope-supplied level.
	if raw != "" {
		up := strings.ToUpper(strings.TrimSpace(raw))
		aliases := map[string]string{}
		for _, m := range aliasEqRe.FindAllStringSubmatch(kql, -1) {
			aliases[m[1]] = m[2]
		}
		for _, m := range aliasInRe.FindAllStringSubmatch(kql, -1) {
			for _, from := range strings.Split(m[1], ",") {
				aliases[strings.Trim(strings.TrimSpace(from), `"`)] = m[2]
			}
		}
		if to, ok := aliases[up]; ok {
			return to
		}
		return up
	}

	// Stage 3: the keyword scan.
	return evalKeywordLadder(kql, msg)
}

// TestLevelExprMatchesClassifierEndToEnd is the coherence guarantee that
// matters: the level a query filters on and the level the response reports are
// produced by one expression, so they must agree for any message.
func TestLevelExprMatchesClassifierEndToEnd(t *testing.T) {
	kql := levelExpr()

	messages := []string{
		// Envelope path.
		`{"level":"error","msg":"boom"}`,
		`{"level":"fatal"}`,
		`{"level":"notice"}`,
		`{"severity":"warning"}`,
		`{"severityText":"TRACE"}`,
		`{"severity_text":"informational"}`,
		// Leading whitespace - the divergence the isolated tests could not see.
		`  {"level":"notice"}`,
		"\n\t{\"level\":\"debug\"}",
		// A non-string level falls through to the next key, then the keywords.
		`{"level":42,"logLevel":"warn"}`,
		`{"level":42}`,
		// An empty level is not a level.
		`{"level":"","msg":"nothing here"}`,
		// Not an object.
		`["level","error"]`,
		`"just a string"`,
		`{not valid json`,
		// Keyword path.
		"an ERROR occurred",
		"INFO: retrying after ERROR",
		"a FATAL condition",
		"SEVERE fault",
		"warning: disk almost full",
		"nothing of note here",
		"",
	}

	for _, msg := range messages {
		want := resolveLogLevel("", msg)
		if got := evalLevelExpr(kql, msg); got != want {
			t.Errorf("message %q: KQL resolves %q, resolveLogLevel resolves %q", msg, got, want)
		}
	}
}

// The image parts are only added when the name does not already carry them, so
// a future agent putting the full reference in `image` cannot produce a
// doubled string.
func TestBuildPlatformLogsKQL_ContainerImageIsIdempotent(t *testing.T) {
	got := BuildPlatformLogsKQL(basePlatformParams())

	for _, want := range []string{
		`_imgName startswith strcat(_imgRepo, "/")`,
		`_imgName contains ":"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("container image assembly is missing the guard %q\n%s", want, got)
		}
	}
}

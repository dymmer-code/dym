package cmd

import (
	"reflect"
	"testing"
)

func TestParseFiltersValidEquality(t *testing.T) {
	got, err := parseFilters([]string{"type=A", "host=www"})
	if err != nil {
		t.Fatal(err)
	}
	want := []fieldFilter{
		{path: "type", value: "A", negate: false},
		{path: "host", value: "www", negate: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseFilters = %+v, want %+v", got, want)
	}
}

func TestParseFiltersValidNegation(t *testing.T) {
	got, err := parseFilters([]string{"type!=A"})
	if err != nil {
		t.Fatal(err)
	}
	want := []fieldFilter{{path: "type", value: "A", negate: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseFilters = %+v, want %+v", got, want)
	}
}

func TestParseFiltersMalformedErrors(t *testing.T) {
	_, err := parseFilters([]string{"no-equals-sign-here"})
	if err == nil {
		t.Fatal("expected error for filter without '='")
	}
}

func TestParseFiltersEmptyInput(t *testing.T) {
	got, err := parseFilters(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("parseFilters(nil) = %+v, want empty", got)
	}
}

func TestGetPathTopLevelField(t *testing.T) {
	row := map[string]any{"id": "r1", "type": "A"}
	v, ok := getPath(row, "type")
	if !ok || v != "A" {
		t.Fatalf("getPath(type) = %v, %v", v, ok)
	}
}

func TestGetPathDottedField(t *testing.T) {
	row := map[string]any{"content": map[string]any{"ip": "203.0.113.10"}}
	v, ok := getPath(row, "content.ip")
	if !ok || v != "203.0.113.10" {
		t.Fatalf("getPath(content.ip) = %v, %v", v, ok)
	}
}

func TestGetPathMissingField(t *testing.T) {
	row := map[string]any{"type": "A"}
	if _, ok := getPath(row, "nonexistent"); ok {
		t.Fatal("expected ok=false for missing field")
	}
	if _, ok := getPath(row, "content.ip"); ok {
		t.Fatal("expected ok=false for missing nested field")
	}
}

func TestMatchesFiltersSingleFilter(t *testing.T) {
	row := map[string]any{"type": "A"}
	if !matchesFilters(row, []fieldFilter{{path: "type", value: "A"}}) {
		t.Fatal("expected match")
	}
	if matchesFilters(row, []fieldFilter{{path: "type", value: "CNAME"}}) {
		t.Fatal("expected no match")
	}
}

func TestMatchesFiltersMultipleANDCombined(t *testing.T) {
	row := map[string]any{"type": "A", "host": "www"}
	filters := []fieldFilter{{path: "type", value: "A"}, {path: "host", value: "www"}}
	if !matchesFilters(row, filters) {
		t.Fatal("expected match when both filters satisfied")
	}
	filters = []fieldFilter{{path: "type", value: "A"}, {path: "host", value: "mail"}}
	if matchesFilters(row, filters) {
		t.Fatal("expected no match when one filter fails")
	}
}

func TestMatchesFiltersNegation(t *testing.T) {
	row := map[string]any{"type": "A"}
	if !matchesFilters(row, []fieldFilter{{path: "type", value: "CNAME", negate: true}}) {
		t.Fatal("expected match: type != CNAME")
	}
	if matchesFilters(row, []fieldFilter{{path: "type", value: "A", negate: true}}) {
		t.Fatal("expected no match: type != A is false")
	}
}

func TestMatchesFiltersMissingFieldNeverMatches(t *testing.T) {
	row := map[string]any{"type": "A"}
	if matchesFilters(row, []fieldFilter{{path: "nonexistent", value: "x"}}) {
		t.Fatal("missing field must not match")
	}
	if matchesFilters(row, []fieldFilter{{path: "nonexistent", value: "x", negate: true}}) {
		t.Fatal("missing field must not match even when negated")
	}
}

func TestMatchesFiltersArrayMembership(t *testing.T) {
	row := map[string]any{"destination": []any{"a@example.com", "b@example.com"}}
	if !matchesFilters(row, []fieldFilter{{path: "destination", value: "b@example.com"}}) {
		t.Fatal("expected membership match")
	}
	if matchesFilters(row, []fieldFilter{{path: "destination", value: "c@example.com"}}) {
		t.Fatal("expected no membership match")
	}
	// Negated membership: true when the value is NOT one of the elements.
	if !matchesFilters(row, []fieldFilter{{path: "destination", value: "c@example.com", negate: true}}) {
		t.Fatal("expected negated membership match when value absent")
	}
	if matchesFilters(row, []fieldFilter{{path: "destination", value: "a@example.com", negate: true}}) {
		t.Fatal("expected no negated membership match when value present")
	}
}

func TestParseSelectSplitsAndTrims(t *testing.T) {
	got := parseSelect("id, type , host")
	want := []string{"id", "type", "host"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSelect = %+v, want %+v", got, want)
	}
}

func TestParseSelectEmptyString(t *testing.T) {
	if got := parseSelect(""); got != nil {
		t.Fatalf("parseSelect(\"\") = %+v, want nil", got)
	}
}

func TestParseSelectDropsEmptyEntries(t *testing.T) {
	got := parseSelect("id,,type,")
	want := []string{"id", "type"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSelect = %+v, want %+v", got, want)
	}
}

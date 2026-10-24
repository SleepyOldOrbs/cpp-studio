package pronunciation

import (
	"strings"
	"testing"
)

func TestResolveLiteralBoundedRules(t *testing.T) {
	rules := []Rule{{"York", "Yawk", ""}, {"New York", "New Yawk", ""}, {"James", "Jaymz", ""}, {"James", "Zhahm", "fr"}, {"café", "ka fay", ""}, {"can't", "cannot", ""}, {"Yawk", "wrong", ""}}
	for _, tc := range []struct{ source, language, want string }{
		{"New York, York Yorkshire YORK", "en", "New Yawk, Yawk Yorkshire YORK"},
		{"James Jameson", "fr", "Zhahm Jameson"},
		{"James", "en", "Jaymz"},
		{"café cafés décafé café́", "en", "ka fay cafés décafé café́"},
		{"can't can'ts", "en", "cannot can'ts"},
	} {
		got, err := Resolve(tc.source, tc.language, rules)
		if err != nil || got != tc.want {
			t.Fatalf("Resolve(%q) = %q, %v; want %q", tc.source, got, err, tc.want)
		}
	}
	result, err := ResolveDetailed("é James!", "en", rules)
	if err != nil || len(result.Substitutions) != 1 {
		t.Fatal(result, err)
	}
	span := result.Substitutions[0]
	if span.SourceStart != 3 || span.SourceEnd != 8 || result.Spoken[span.SpokenStart:span.SpokenEnd] != "Jaymz" {
		t.Fatal(span)
	}
}

func TestFreezeValidationAndCanonicalIdentity(t *testing.T) {
	a, err := Freeze([]Rule{{"b", "bee", "EN"}, {"a", "ay", ""}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Freeze([]Rule{{"a", "ay", ""}, {"b", "bee", "en"}})
	if err != nil || a.Fingerprint != b.Fingerprint {
		t.Fatal(a, b, err)
	}
	for _, rules := range [][]Rule{{{"", "x", ""}}, {{"x", "", ""}}, {{strings.Repeat("é", 121), "x", ""}}, {{"x", strings.Repeat("a", 241), ""}}, {{"x", "y", ""}, {"x", "z", ""}}, {{"x", "y", "en invalid"}}, make([]Rule, 1001)} {
		if _, err := Freeze(rules); err == nil {
			t.Fatalf("accepted invalid rules: %.80v", rules)
		}
	}
	if _, err := ValidateSnapshot(&Snapshot{Rules: a.Rules, Fingerprint: "wrong", PolicyVersion: a.PolicyVersion}); err == nil {
		t.Fatal("accepted altered snapshot")
	}
}

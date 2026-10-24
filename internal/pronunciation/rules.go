// Package pronunciation owns literal respelling rules shared by saved productions.
package pronunciation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const PolicyVersion = 1

type Rule struct {
	Term        string `json:"term"`
	Replacement string `json:"replacement"`
	Language    string `json:"language,omitempty"`
}
type Snapshot struct {
	Rules         []Rule `json:"rules"`
	Fingerprint   string `json:"fingerprint"`
	PolicyVersion int    `json:"policyVersion"`
}
type Substitution struct {
	SourceStart int `json:"sourceStart"`
	SourceEnd   int `json:"sourceEnd"`
	SpokenStart int `json:"spokenStart"`
	SpokenEnd   int `json:"spokenEnd"`
}
type Resolution struct {
	Source        string         `json:"source"`
	Spoken        string         `json:"spoken"`
	Substitutions []Substitution `json:"substitutions"`
}

func Language(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 35 {
		return "", fmt.Errorf("language code is too long")
	}
	for i, r := range value {
		if !(r >= 'a' && r <= 'z' || i > 0 && (r == '-' || r >= '0' && r <= '9')) {
			return "", fmt.Errorf("use a language code such as en or en-gb")
		}
	}
	return value, nil
}
func Freeze(rules []Rule) (Snapshot, error) {
	if len(rules) > 1000 {
		return Snapshot{}, fmt.Errorf("at most 1000 pronunciation rules are allowed")
	}
	out := Snapshot{Rules: append([]Rule{}, rules...), PolicyVersion: PolicyVersion}
	seen := map[string]bool{}
	for i := range out.Rules {
		r := &out.Rules[i]
		if !utf8.ValidString(r.Term) || !utf8.ValidString(r.Replacement) || strings.ContainsRune(r.Term, 0) || strings.ContainsRune(r.Replacement, 0) || strings.TrimSpace(r.Term) == "" || strings.TrimSpace(r.Replacement) == "" || utf8.RuneCountInString(r.Term) > 120 || utf8.RuneCountInString(r.Replacement) > 240 {
			return Snapshot{}, fmt.Errorf("rule %d requires a term (1–120 characters) and respelling (1–240 characters)", i+1)
		}
		var err error
		r.Language, err = Language(r.Language)
		if err != nil {
			return Snapshot{}, err
		}
		key := r.Term + "\x00" + r.Language
		if seen[key] {
			return Snapshot{}, fmt.Errorf("duplicate term and language: %s", r.Term)
		}
		seen[key] = true
	}
	sort.Slice(out.Rules, func(i, j int) bool {
		a, b := out.Rules[i], out.Rules[j]
		if a.Term != b.Term {
			return a.Term < b.Term
		}
		return a.Language < b.Language
	})
	data, _ := json.Marshal(struct {
		Policy int
		Rules  []Rule
	}{PolicyVersion, out.Rules})
	sum := sha256.Sum256(data)
	out.Fingerprint = hex.EncodeToString(sum[:])
	return out, nil
}

// ValidateSnapshot returns an owned canonical copy, rejecting tampering and unsupported policies.
func ValidateSnapshot(snapshot *Snapshot) (*Snapshot, error) {
	if snapshot == nil {
		return nil, nil
	}
	frozen, err := Freeze(snapshot.Rules)
	if err != nil {
		return nil, err
	}
	if snapshot.PolicyVersion != PolicyVersion || snapshot.Fingerprint != frozen.Fingerprint {
		return nil, fmt.Errorf("pronunciation snapshot identity or policy is invalid")
	}
	if len(frozen.Rules) == 0 {
		return nil, nil
	}
	return &frozen, nil
}
func Resolve(source, language string, rules []Rule) (string, error) {
	result, err := ResolveDetailed(source, language, rules)
	return result.Spoken, err
}
func ResolveDetailed(source, language string, rules []Rule) (Resolution, error) {
	result := Resolution{Source: source, Substitutions: []Substitution{}}
	if !utf8.ValidString(source) {
		return result, fmt.Errorf("source must be UTF-8")
	}
	language, err := Language(language)
	if err != nil {
		return result, err
	}
	frozen, err := Freeze(rules)
	if err != nil {
		return result, err
	}
	matching := []Rule{}
	for _, r := range frozen.Rules {
		if r.Language == "" || r.Language == language {
			matching = append(matching, r)
		}
	}
	sort.SliceStable(matching, func(i, j int) bool {
		if len(matching[i].Term) != len(matching[j].Term) {
			return len(matching[i].Term) > len(matching[j].Term)
		}
		return matching[i].Language != "" && matching[j].Language == ""
	})
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) }
	var spoken strings.Builder
	for pos := 0; pos < len(source); {
		matched := false
		for _, r := range matching {
			if !strings.HasPrefix(source[pos:], r.Term) {
				continue
			}
			end := pos + len(r.Term)
			if pos > 0 {
				prev, _ := utf8.DecodeLastRuneInString(source[:pos])
				if word(prev) {
					continue
				}
			}
			if end < len(source) {
				next, _ := utf8.DecodeRuneInString(source[end:])
				if word(next) {
					continue
				}
			}
			start := spoken.Len()
			spoken.WriteString(r.Replacement)
			result.Substitutions = append(result.Substitutions, Substitution{pos, end, start, spoken.Len()})
			pos = end
			matched = true
			break
		}
		if !matched {
			_, size := utf8.DecodeRuneInString(source[pos:])
			spoken.WriteString(source[pos : pos+size])
			pos += size
		}
	}
	result.Spoken = spoken.String()
	return result, nil
}

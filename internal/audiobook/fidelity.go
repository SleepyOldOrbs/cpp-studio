package audiobook

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const FidelityWERThreshold = 0.12

var fidelityTokenRE = regexp.MustCompile(`[\p{L}\p{N}]+(?:['’-][\p{L}\p{N}]+)*`)

// Verification is the transport-neutral result returned by an injected ASR
// verifier. Audiobook owns all comparison policy and durable report types.
type Verification struct {
	Transcript       string    `json:"transcript"`
	VerifierIdentity string    `json:"verifierIdentity"`
	VerifiedAt       time.Time `json:"verifiedAt"`
}

type VerifyFunc func(ctx context.Context, source string, wav []byte) (Verification, error)

type FidelityReport struct {
	SectionID             string        `json:"sectionId"`
	SourceSHA256          string        `json:"sourceSha256"`
	VerifierIdentity      string        `json:"verifierIdentity"`
	VerifiedAt            time.Time     `json:"verifiedAt"`
	SourceTokens          int           `json:"sourceTokens"`
	TranscriptTokens      int           `json:"transcriptTokens"`
	Insertions            int           `json:"insertions"`
	Deletions             int           `json:"deletions"`
	Substitutions         int           `json:"substitutions"`
	WordErrorRate         float64       `json:"wordErrorRate"`
	MissingNumericOrDates []string      `json:"missingNumericOrDates,omitempty"`
	MissingAcronyms       []string      `json:"missingAcronyms,omitempty"`
	MissingProperNames    []string      `json:"missingProperNames,omitempty"`
	Status                SectionStatus `json:"status"`
}

type FidelityAggregate struct {
	Mode             VerificationMode   `json:"mode"`
	Status           VerificationStatus `json:"status"`
	VerifiedSections int                `json:"verifiedSections"`
	FlaggedSections  int                `json:"flaggedSections"`
	Error            string             `json:"error,omitempty"`
	Sections         []FidelityReport   `json:"sections,omitempty"`
}

func evaluateFidelity(section Section, source string, verification Verification, now time.Time) FidelityReport {
	if verification.VerifiedAt.IsZero() {
		verification.VerifiedAt = now
	}
	sourceTokens := normalizedTokens(source)
	transcriptTokens := normalizedTokens(verification.Transcript)
	insertions, deletions, substitutions := editCounts(sourceTokens, transcriptTokens)
	errors := insertions + deletions + substitutions
	denominator := len(sourceTokens)
	if denominator == 0 {
		denominator = 1
	}
	report := FidelityReport{
		SectionID: section.ID, SourceSHA256: section.TextSHA256,
		VerifierIdentity: verification.VerifierIdentity, VerifiedAt: verification.VerifiedAt,
		SourceTokens: len(sourceTokens), TranscriptTokens: len(transcriptTokens),
		Insertions: insertions, Deletions: deletions, Substitutions: substitutions,
		WordErrorRate:         float64(errors) / float64(denominator),
		MissingNumericOrDates: missingAnchors(numericTokens(source), transcriptTokens),
		MissingAcronyms:       missingAnchors(acronymTokens(source), transcriptTokens),
		MissingProperNames:    missingAnchors(properNameTokens(source), transcriptTokens),
		Status:                SectionStatusVerified,
	}
	if report.WordErrorRate > FidelityWERThreshold || len(report.MissingNumericOrDates)+len(report.MissingAcronyms)+len(report.MissingProperNames) > 0 {
		report.Status = SectionStatusFlagged
	}
	return report
}

func normalizedTokens(text string) []string {
	raw := fidelityTokenRE.FindAllString(text, -1)
	for i := range raw {
		raw[i] = strings.ToLower(strings.ReplaceAll(raw[i], "’", "'"))
	}
	return raw
}

func numericTokens(text string) []string {
	var out []string
	for _, token := range fidelityTokenRE.FindAllString(text, -1) {
		if strings.IndexFunc(token, unicode.IsDigit) >= 0 {
			out = append(out, strings.ToLower(token))
		}
	}
	return unique(out)
}

func acronymTokens(text string) []string {
	var out []string
	for _, token := range fidelityTokenRE.FindAllString(text, -1) {
		letters := 0
		upper := true
		for _, r := range token {
			if unicode.IsLetter(r) {
				letters++
				upper = upper && unicode.IsUpper(r)
			}
		}
		if upper && letters >= 2 {
			out = append(out, strings.ToLower(token))
		}
	}
	return unique(out)
}

func properNameTokens(text string) []string {
	indices := fidelityTokenRE.FindAllStringIndex(text, -1)
	var out []string
	for _, bounds := range indices {
		token := text[bounds[0]:bounds[1]]
		first, _ := utf8.DecodeRuneInString(token)
		if !unicode.IsUpper(first) || strings.ToUpper(token) == token || sentenceStart(text, bounds[0]) {
			continue
		}
		out = append(out, strings.ToLower(token))
	}
	return unique(out)
}

func sentenceStart(text string, start int) bool {
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		start -= size
		if unicode.IsSpace(r) {
			continue
		}
		return r == '.' || r == '!' || r == '?'
	}
	return true
}

func missingAnchors(source, transcript []string) []string {
	available := make(map[string]int, len(transcript))
	for _, token := range transcript {
		available[token]++
	}
	var missing []string
	for _, token := range source {
		if available[token] == 0 {
			missing = append(missing, token)
		} else {
			available[token]--
		}
	}
	return missing
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func editCounts(source, transcript []string) (insertions, deletions, substitutions int) {
	type cell struct{ cost, insertions, deletions, substitutions int }
	rows := make([][]cell, len(source)+1)
	for i := range rows {
		rows[i] = make([]cell, len(transcript)+1)
		rows[i][0] = cell{cost: i, deletions: i}
	}
	for j := range rows[0] {
		rows[0][j] = cell{cost: j, insertions: j}
	}
	better := func(a, b cell) cell {
		if a.cost != b.cost {
			if a.cost < b.cost {
				return a
			}
			return b
		}
		// Stable tie-break: substitutions, deletions, then insertions.
		if a.substitutions != b.substitutions {
			if a.substitutions < b.substitutions {
				return a
			}
			return b
		}
		if a.deletions != b.deletions {
			if a.deletions < b.deletions {
				return a
			}
			return b
		}
		if a.insertions < b.insertions {
			return a
		}
		return b
	}
	for i := 1; i <= len(source); i++ {
		for j := 1; j <= len(transcript); j++ {
			if source[i-1] == transcript[j-1] {
				rows[i][j] = rows[i-1][j-1]
				continue
			}
			sub := rows[i-1][j-1]
			sub.cost++
			sub.substitutions++
			del := rows[i-1][j]
			del.cost++
			del.deletions++
			ins := rows[i][j-1]
			ins.cost++
			ins.insertions++
			rows[i][j] = better(sub, better(del, ins))
		}
	}
	result := rows[len(source)][len(transcript)]
	return result.insertions, result.deletions, result.substitutions
}

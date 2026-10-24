package engine

import "testing"

func TestValidateSpeechDirectionRejectsUnsupportedOmniVoiceStylesBeforeInvocation(t *testing.T) {
	for _, tc := range []struct {
		engine, direction string
		valid             bool
	}{
		{"omnivoice", "", true}, {"omnivoice", "whisper, low pitch", true},
		{"omnivoice", "elderly，British accent", true}, {"omnivoice", "耳语, 低音调", true},
		{"omnivoice", "warm, curious", false}, {"omnivoice", "male, female", false},
		{"omnivoice", "low pitch, high pitch", false}, {"fish-drama3", "softly, with relief", true},
	} {
		if err := ValidateSpeechDirection(tc.engine, tc.direction); (err == nil) != tc.valid {
			t.Errorf("%s %q: %v, valid=%v", tc.engine, tc.direction, err, tc.valid)
		}
	}
}

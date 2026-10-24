package audiobook

import (
	"context"
	"cpp-studio/internal/jobs"
	"cpp-studio/internal/pronunciation"
	"cpp-studio/internal/wav"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCorrectedPlanningPreservesSourceAndSpokenLimits(t *testing.T) {
	snapshot, _ := pronunciation.Freeze([]pronunciation.Rule{{Term: "New York", Replacement: strings.Repeat("wide ", 40), Language: "en"}})
	source := strings.Repeat("The city New York is quiet. ", 15)
	req, err := NormalizeRequest(Request{Text: source, Pronunciation: &snapshot, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	sections, err := planCorrectedSections(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	var original, spoken strings.Builder
	for _, s := range sections {
		original.WriteString(source[s.StartByte:s.EndByte])
		spoken.WriteString(s.SpokenText)
		if len([]rune(s.SpokenText)) > DefaultChunkChars || s.SpokenSHA256 == "" {
			t.Fatal(s)
		}
	}
	expected, _ := pronunciation.Resolve(source, "en", snapshot.Rules)
	if original.String() != source || spoken.String() != expected {
		t.Fatal("source or single-pass resolution changed")
	}
	old := buildSynthesisIdentity(Request{Text: source}, EngineIdentity{}, VoiceIdentity{})
	corrected := buildSynthesisIdentity(req, EngineIdentity{}, VoiceIdentity{})
	if old.SourceSHA256 != corrected.SourceSHA256 || old.Fingerprint == corrected.Fingerprint {
		t.Fatal("incorrect source/spoken identity")
	}
}

func TestCorrectedWhitespaceCannotOverflowSpokenBudget(t *testing.T) {
	snapshot, _ := pronunciation.Freeze([]pronunciation.Rule{{Term: "James", Replacement: "Jaymz"}})
	req, _ := NormalizeRequest(Request{Text: "James" + strings.Repeat(" ", 400), Pronunciation: &snapshot, Language: "en"})
	sections, err := planCorrectedSections(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range sections {
		if len([]rune(section.SpokenText)) > DefaultChunkChars {
			t.Fatal("whitespace exceeded narrator budget", len([]rune(section.SpokenText)))
		}
	}
}

func TestCorrectedResumeRetryAndFidelityKeepFrozenSpeech(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	snapshot, _ := pronunciation.Freeze([]pronunciation.Rule{{Term: "James", Replacement: "Jaymz", Language: "en"}})
	source := "# One\nJames opens the door.\n# Two\nJames leaves the room."
	m := NewManager(ManagerOptions{RootDir: root, Jobs: registry, Synthesize: func(ctx context.Context, req SynthesisRequest) ([]byte, error) {
		if !strings.Contains(req.Text, "Jaymz") || strings.Contains(req.Text, "James") {
			t.Errorf("wrong spoken input %q", req.Text)
		}
		if calls.Add(1) > 1 {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return wav.SyntheticTone(16000), nil
	}})
	doc := DocumentRequest{Filename: "book.md", Data: []byte(source), Request: Request{EngineID: DramaBoxEngineID, Pronunciation: &snapshot, Language: "en", Verification: VerificationModeAuto}}
	preview, err := m.PreviewDocument(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Plan.Chapters[0].SpokenExcerpt == "" {
		t.Fatal("missing corrected preview")
	}
	id, _, err := m.Create(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	if err = m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	waitForAudiobookTerminal(t, registry, id)
	// Editing the original caller's/global dictionary must not mutate the frozen work.
	snapshot.Rules[0].Replacement = "Wrong"
	global := pronunciation.NewStore(filepath.Join(root, "dictionary.json"))
	if _, err = global.Replace(pronunciation.Document{Version: 1, Rules: snapshot.Rules}); err != nil {
		t.Fatal(err)
	}
	reg2 := jobs.NewRegistry()
	var expectedText string
	resumed := NewManager(ManagerOptions{RootDir: root, Jobs: reg2, Synthesize: func(_ context.Context, req SynthesisRequest) ([]byte, error) {
		if !strings.Contains(req.Text, "Jaymz") || strings.Contains(req.Text, "Wrong") {
			t.Errorf("resume changed speech: %q", req.Text)
		}
		return wav.SyntheticTone(16000), nil
	}, Verify: func(_ context.Context, text string, _ []byte) (Verification, error) {
		expectedText = text
		return Verification{Transcript: text}, nil
	}})
	if _, err = resumed.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, reg2, id)
	manifest, original, err := resumed.store.LoadDurableFinal(id)
	if err != nil {
		t.Fatal(err)
	}
	if original != source || manifest.Pronunciation.Rules[0].Replacement != "Jaymz" {
		t.Fatal("frozen source/rules changed")
	}
	retryID, err := resumed.RetrySection(context.Background(), id, manifest.Sections[0].ID, RetryModeReproduce)
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, reg2, retryID)
	if !strings.Contains(expectedText, "Jaymz") || strings.Contains(expectedText, "James") {
		t.Fatal("fidelity did not compare resolved text", expectedText)
	}
	bad := manifest
	bad.Sections = append([]Section(nil), manifest.Sections...)
	bad.Sections[0].SpokenText = "tampered"
	if err = validateDurableManifest(bad, source); err == nil {
		t.Fatal("altered spoken checkpoint accepted")
	}
}

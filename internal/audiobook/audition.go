package audiobook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cpp-studio/internal/wav"
)

// AuditionDocument shares production resolution and its displayed, bounded
// source excerpt. Only the latest successful audition is retained in memory.
func (m *Manager) AuditionDocument(ctx context.Context, document DocumentRequest, chapterID string) (string, error) {
	if m.synthesizeDetailed == nil {
		return "", requestErrorf("audition needs a configured speech engine")
	}
	resolved, err := m.PreviewDocument(ctx, document)
	if err != nil {
		return "", err
	}
	if document.Request.PlanDigest != "" && document.Request.PlanDigest != resolved.Plan.Digest {
		return "", requestErrorf("chapter plan changed; preview the document and settings again")
	}
	passage := ""
	for _, chapter := range resolved.Plan.Chapters {
		if chapter.ID == chapterID {
			passage = chapter.Excerpt
			if resolved.Request.Pronunciation != nil {
				passage = chapter.SpokenExcerpt
			}
			break
		}
	}
	if passage == "" {
		return "", requestErrorf("choose a chapter from the current plan")
	}
	request := resolved.Request
	if request.EngineID == DramaBoxEngineID {
		evaluation, err := EvaluateDramaBoxPrompt(request.PromptSpec, request.Text)
		if err != nil {
			return "", err
		}
		if len(evaluation.Warnings) > 0 && !request.AcceptPromptWarnings {
			return "", requestErrorf("preview and accept the prompt warnings before auditioning")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID != "" || m.creating {
		return "", fmt.Errorf("another audiobook is already narrating")
	}
	options := request.Options
	if request.EngineID == DramaBoxEngineID {
		options.Seed, err = readSectionSeed(m.seedSource)
		if err != nil {
			return "", err
		}
	}
	var release func()
	if m.reserveEngine != nil {
		var ok bool
		release, ok = m.reserveEngine(ctx, request.EngineID)
		if !ok {
			return "", fmt.Errorf("engine %q is busy", request.EngineID)
		}
	}
	m.auditionCounter++
	id := fmt.Sprintf("audition_%s_%03d", m.now().Format("20060102_150405"), m.auditionCounter)
	jobCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	m.activeID = id
	m.cancels[id] = cancel
	m.auditionID, m.auditionAudio = "", nil
	if m.registry != nil {
		m.registry.Track(id, "audiobook-audition", func() { _ = m.Cancel(id) })
	}
	go func() {
		defer func() {
			cancel()
			if release != nil {
				release()
			}
			m.mu.Lock()
			if m.activeID == id {
				m.activeID = ""
			}
			delete(m.cancels, id)
			m.mu.Unlock()
		}()
		if m.registry != nil {
			m.registry.Update(id, .1, "auditioning source passage")
		}
		text := passage
		if request.EngineID == DramaBoxEngineID {
			text = buildRequestPrompt(request, passage)
		}
		var err error
		var result SynthesisResult
		if err == nil {
			result, err = m.invokeSynthesis(jobCtx, SynthesisRequest{Text: text, VoiceID: request.VoiceID, EngineID: request.EngineID, Options: options, Voice: resolved.Voice.Reference})
		}
		if err == nil {
			err = jobCtx.Err()
		}
		if err == nil && len(result.Audio) > 16*1024*1024 {
			err = fmt.Errorf("audition exceeds the temporary audio limit")
		}
		if err == nil {
			err = wav.ValidateBytes(result.Audio)
		}
		if err != nil {
			if m.registry != nil {
				if errors.Is(jobCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
					m.registry.MarkCancelled(id)
				} else {
					m.registry.Fail(id, err.Error())
				}
			}
			return
		}
		m.mu.Lock()
		m.auditionID, m.auditionAudio = id, append([]byte(nil), result.Audio...)
		m.mu.Unlock()
		if m.registry != nil {
			m.registry.Complete(id, map[string]string{"audioUrl": "/v1/audiobooks/auditions/" + id + "/audio", "excerpt": passage})
		}
	}()
	return id, nil
}

func (m *Manager) AuditionAudio(id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != m.auditionID || len(m.auditionAudio) == 0 {
		return nil, ErrProductionNotFound
	}
	return append([]byte(nil), m.auditionAudio...), nil
}

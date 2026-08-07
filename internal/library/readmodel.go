package library

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"cpp-studio/internal/audiobook"
	"cpp-studio/internal/story"
	"cpp-studio/internal/storybuilder"
	"cpp-studio/internal/voice"
)

const (
	KindActorVoice          = "actor_voice"
	KindCharacterVoice      = "character_voice"
	KindReusableAudio       = "reusable_audio"
	KindSavedImage          = "saved_image"
	KindStory               = "story"
	KindStoryBuilderProject = "story_builder_project"
	KindAudiobook           = "audiobook"
	KindRenderRevision      = "render_revision"
	KindMixedMaster         = "mixed_master"
	KindExport              = "export"
)

type Action struct {
	Label          string `json:"label"`
	URL            string `json:"url"`
	Method         string `json:"method,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

type Relationship struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Entry is the stable Library read shape. It points back to owning routes;
// the Library never copies a purpose-built manifest or owns its mutations.
type Entry struct {
	Kind           string            `json:"kind"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Subtype        string            `json:"subtype,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at,omitempty"`
	Relationship   *Relationship     `json:"relationship,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	PreviewAction  *Action           `json:"preview_action,omitempty"`
	ArtifactAction *Action           `json:"artifact_action,omitempty"`
	LaunchAction   *Action           `json:"launch_action,omitempty"`
	DeleteAction   *Action           `json:"delete_action,omitempty"`
	Children       []Entry           `json:"children,omitempty"`
	search         []string
}

type ReadResponse struct {
	Entries []Entry `json:"entries"`
	// Items preserves the original saved-audio/image contract for existing
	// Story Builder and console consumers while entries becomes the browse API.
	Items []Item `json:"items"`
}

type ReadModelSources struct {
	Items           func() ([]Item, error)
	ActorVoices     func() ([]voice.Clone, error)
	CharacterVoices func(string) ([]voice.CharacterVoice, error)
	Stories         func() ([]story.Summary, error)
	Projects        func() ([]storybuilder.Project, error)
	Audiobooks      func() ([]audiobook.Manifest, error)
}

type ReadModel struct {
	sources ReadModelSources
}

func NewReadModel(sources ReadModelSources) *ReadModel {
	return &ReadModel{sources: sources}
}

func (m *ReadModel) List(query string) (ReadResponse, error) {
	items, err := m.sources.Items()
	if err != nil {
		return ReadResponse{}, fmt.Errorf("list saved Library items: %w", err)
	}
	actors, err := m.sources.ActorVoices()
	if err != nil {
		return ReadResponse{}, fmt.Errorf("list Actor Voices: %w", err)
	}
	stories, err := m.sources.Stories()
	if err != nil {
		return ReadResponse{}, fmt.Errorf("list Stories: %w", err)
	}
	projects, err := m.sources.Projects()
	if err != nil {
		return ReadResponse{}, fmt.Errorf("list Story Builder Projects: %w", err)
	}
	books, err := m.sources.Audiobooks()
	if err != nil {
		return ReadResponse{}, fmt.Errorf("list Audiobooks: %w", err)
	}

	entries := make([]Entry, 0, len(items)+len(actors)+len(stories)+len(projects)+len(books))
	for _, item := range items {
		entries = append(entries, savedItemEntry(item))
	}
	for _, actor := range actors {
		characters, err := m.sources.CharacterVoices(actor.ID)
		if err != nil {
			return ReadResponse{}, fmt.Errorf("list Character Voices for %s: %w", actor.ID, err)
		}
		entries = append(entries, actorVoiceEntry(actor, characters))
	}
	for _, item := range stories {
		entries = append(entries, storyEntries(item)...)
	}
	for _, project := range projects {
		entries = append(entries, projectEntries(project)...)
	}
	for _, book := range books {
		entries = append(entries, audiobookEntries(book)...)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entryTime(entries[i]), entryTime(entries[j])
		if left.Equal(right) {
			return entries[i].Kind+entries[i].ID < entries[j].Kind+entries[j].ID
		}
		return left.After(right)
	})
	entries = filterEntries(entries, query)
	if entries == nil {
		entries = []Entry{}
	}
	if items == nil {
		items = []Item{}
	}
	return ReadResponse{Entries: entries, Items: items}, nil
}

func savedItemEntry(item Item) Entry {
	artifactURL := "/v1/library/" + item.ID + "/artifact"
	kind, subtype, contentType := KindSavedImage, "png", "image/png"
	if item.Kind == "audio" {
		kind, subtype, contentType = KindReusableAudio, string(item.MediaRole), "audio/wav"
	}
	metadata := copyMetadata(item.Meta)
	metadata["bytes"] = strconv.FormatInt(item.Bytes, 10)
	if item.DurationMS > 0 {
		metadata["duration_ms"] = strconv.FormatInt(item.DurationMS, 10)
	}
	entry := Entry{
		Kind: kind, ID: item.ID, Name: item.Name, Subtype: subtype, CreatedAt: item.CreatedAt, Metadata: metadata,
		PreviewAction:  &Action{Label: "Preview", URL: artifactURL, ContentType: contentType},
		ArtifactAction: &Action{Label: "Download", URL: artifactURL, ContentType: contentType},
		LaunchAction:   &Action{Label: "Open Library", URL: "/demo/#library"},
		DeleteAction:   &Action{Label: "Delete", URL: "/v1/library/" + item.ID, Method: "DELETE"},
		search:         []string{item.Name, item.Kind, subtype, strings.Join(metadataValues(metadata), " ")},
	}
	return entry
}

func actorVoiceEntry(actor voice.Clone, characters []voice.CharacterVoice) Entry {
	audioURL := "/v1/voices/" + actor.ID + "/audio"
	entry := Entry{
		Kind: KindActorVoice, ID: actor.ID, Name: actor.Name, Subtype: "actor", CreatedAt: actor.CreatedAt,
		Metadata:       map[string]string{"transcript": actor.Transcript},
		PreviewAction:  &Action{Label: "Preview", URL: audioURL, ContentType: "audio/wav"},
		ArtifactAction: &Action{Label: "Download reference", URL: audioURL, ContentType: "audio/wav"},
		LaunchAction:   &Action{Label: "Open Voice cloning", URL: "/demo/#voice-cloning"},
		DeleteAction:   &Action{Label: "Delete", URL: "/v1/voices/" + actor.ID, Method: "DELETE"},
		search:         []string{actor.Name, actor.ID, actor.Transcript},
	}
	if actor.Source != nil {
		entry.Metadata["source_name"] = actor.Source.Name
		entry.search = append(entry.search, actor.Source.Name, actor.Source.Speaker)
	}
	if actor.Analysis != nil {
		entry.Metadata["duration_seconds"] = strconv.FormatFloat(actor.Analysis.DurationSeconds, 'f', 1, 64)
		entry.Metadata["fitness"] = actor.Analysis.Fitness
		entry.search = append(entry.search, actor.Analysis.Fitness)
	}
	if actor.Protected {
		entry.DeleteAction.DisabledReason = "This protected Actor Voice cannot be deleted"
	} else if len(characters) > 0 {
		entry.DeleteAction.DisabledReason = "Delete its Character Voices first"
	}
	for _, character := range characters {
		child := Entry{
			Kind: KindCharacterVoice, ID: character.ID, Name: character.Name, Subtype: "character",
			CreatedAt: character.CreatedAt, UpdatedAt: character.UpdatedAt,
			Relationship: &Relationship{Kind: KindActorVoice, ID: actor.ID, Name: actor.Name},
			Metadata:     map[string]string{"direction": character.Direction},
			LaunchAction: &Action{Label: "Open Actor Voice", URL: "/demo/#voice-cloning"},
			DeleteAction: &Action{Label: "Delete", URL: "/v1/character-voices/" + character.ID, Method: "DELETE"},
			search:       []string{character.Name, character.ID, character.Direction, actor.Name, actor.ID},
		}
		if character.Preview != nil {
			child.PreviewAction = &Action{Label: "Preview", URL: "/v1/character-voices/" + character.ID + "/preview/audio", ContentType: "audio/wav"}
			child.Metadata["sample_text"] = character.Preview.SampleText
		}
		entry.Children = append(entry.Children, child)
		if character.UpdatedAt.After(entry.UpdatedAt) {
			entry.UpdatedAt = character.UpdatedAt
		}
	}
	return entry
}

func storyEntries(item story.Summary) []Entry {
	name := item.Title
	if name == "" {
		name = item.Subject
	}
	entry := Entry{
		Kind: KindStory, ID: item.ID, Name: name, Subtype: item.Mode, CreatedAt: item.CreatedAt,
		Metadata:     map[string]string{"subject": item.Subject, "status": string(item.Status), "duration_seconds": strconv.Itoa(item.DurationSeconds)},
		LaunchAction: &Action{Label: "Open Story", URL: "/demo/#story"},
		DeleteAction: &Action{Label: "Delete", URL: "/v1/stories/" + item.ID, Method: "DELETE"},
		search:       []string{name, item.Subject, item.Mode, string(item.Status)},
	}
	if item.Status == story.StatusInterrupted {
		entry.DeleteAction = &Action{Label: "Discard", URL: "/v1/stories/" + item.ID + "/discard", Method: "POST"}
	}
	if item.ArtifactURL != "" {
		entry.PreviewAction = &Action{Label: "Preview", URL: item.ArtifactURL, ContentType: "audio/wav"}
		entry.ArtifactAction = &Action{Label: "Download WAV", URL: item.ArtifactURL, ContentType: "audio/wav"}
	}
	entries := []Entry{entry}
	for _, render := range item.Renders {
		renderID := fmt.Sprintf("%s/render/%d", item.ID, render.Revision)
		renderName := fmt.Sprintf("%s — render r%d", name, render.Revision)
		entries = append(entries, Entry{
			Kind: KindRenderRevision, ID: renderID, Name: renderName, Subtype: "wav", CreatedAt: render.CreatedAt,
			Relationship:   &Relationship{Kind: KindStory, ID: item.ID, Name: name},
			Metadata:       map[string]string{"duration_seconds": strconv.Itoa(render.DurationSeconds), "revision": strconv.Itoa(render.Revision)},
			PreviewAction:  &Action{Label: "Preview", URL: render.URL, ContentType: "audio/wav"},
			ArtifactAction: &Action{Label: "Download WAV", URL: render.URL, ContentType: "audio/wav"},
			LaunchAction:   &Action{Label: "Open Story", URL: "/demo/#story"},
			search:         []string{name, item.Subject, "render revision", "wav", strconv.Itoa(render.Revision)},
		})
		for _, export := range render.Exports {
			entries = append(entries, Entry{
				Kind: KindExport, ID: fmt.Sprintf("%s/export/%s", renderID, export.Format),
				Name: renderName + " — " + strings.ToUpper(export.Format), Subtype: export.Format, CreatedAt: export.CreatedAt,
				Relationship:   &Relationship{Kind: KindRenderRevision, ID: renderID, Name: renderName},
				Metadata:       map[string]string{"bitrate": export.Bitrate, "revision": strconv.Itoa(render.Revision)},
				ArtifactAction: &Action{Label: "Download " + strings.ToUpper(export.Format), URL: export.URL, ContentType: deliveryContentType(export.Format)},
				LaunchAction:   &Action{Label: "Open Story", URL: "/demo/#story"},
				search:         []string{name, item.Subject, export.Format, export.Bitrate, "export", strconv.Itoa(render.Revision)},
			})
		}
	}
	return entries
}

func projectEntries(project storybuilder.Project) []Entry {
	launchURL := "/demo/story-builder.html?project=" + project.ID
	projectEntry := Entry{
		Kind: KindStoryBuilderProject, ID: project.ID, Name: project.Name, CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt,
		Metadata:     map[string]string{"tracks": strconv.Itoa(len(project.Tracks)), "revision": strconv.Itoa(project.Revision), "duration_ms": strconv.FormatInt(project.TimelineDurationMS, 10)},
		LaunchAction: &Action{Label: "Open Story Builder", URL: launchURL},
		DeleteAction: &Action{Label: "Delete", URL: "/v1/story-builder-projects/" + project.ID, Method: "DELETE"},
		search:       []string{project.Name, project.ID, "Story Builder Project"},
	}
	for _, track := range project.Tracks {
		projectEntry.search = append(projectEntry.search, track.Name)
	}
	entries := []Entry{projectEntry}
	for _, render := range project.Renders {
		masterID := fmt.Sprintf("%s/render/%d", project.ID, render.Revision)
		masterName := fmt.Sprintf("%s — master r%d", project.Name, render.Revision)
		entries = append(entries, Entry{
			Kind: KindMixedMaster, ID: masterID, Name: masterName, Subtype: "wav", CreatedAt: render.CreatedAt,
			Relationship:   &Relationship{Kind: KindStoryBuilderProject, ID: project.ID, Name: project.Name},
			Metadata:       map[string]string{"duration_ms": strconv.FormatInt(render.DurationMS, 10), "revision": strconv.Itoa(render.Revision)},
			PreviewAction:  &Action{Label: "Preview", URL: render.URL, ContentType: "audio/wav"},
			ArtifactAction: &Action{Label: "Download WAV", URL: render.URL, ContentType: "audio/wav"},
			LaunchAction:   &Action{Label: "Open Story Builder", URL: launchURL},
			search:         []string{project.Name, "mixed master", "wav", strconv.Itoa(render.Revision)},
		})
		for _, export := range render.Exports {
			entries = append(entries, Entry{
				Kind: KindExport, ID: masterID + "/export/" + export.Format,
				Name: masterName + " — " + strings.ToUpper(export.Format), Subtype: export.Format, CreatedAt: export.CreatedAt,
				Relationship:   &Relationship{Kind: KindMixedMaster, ID: masterID, Name: masterName},
				Metadata:       map[string]string{"bitrate": export.Bitrate, "revision": strconv.Itoa(render.Revision)},
				ArtifactAction: &Action{Label: "Download " + strings.ToUpper(export.Format), URL: export.URL, ContentType: deliveryContentType(export.Format)},
				LaunchAction:   &Action{Label: "Open Story Builder", URL: launchURL},
				search:         []string{project.Name, "export", export.Format, export.Bitrate, strconv.Itoa(render.Revision)},
			})
		}
	}
	return entries
}

func audiobookEntries(book audiobook.Manifest) []Entry {
	status := string(book.Status)
	if status == "" {
		status = "complete"
	}
	entry := Entry{
		Kind: KindAudiobook, ID: book.ID, Name: book.Title, Subtype: status, CreatedAt: book.CreatedAt,
		Metadata:     map[string]string{"status": status, "engine": book.EngineID, "direction": book.Direction, "duration_seconds": strconv.Itoa(book.DurationSeconds)},
		LaunchAction: &Action{Label: "Open Audiobook", URL: "/demo/#audiobook"},
		DeleteAction: &Action{Label: "Delete", URL: "/v1/audiobooks/" + book.ID, Method: "DELETE"},
		search:       []string{book.Title, book.ID, status, book.EngineID, book.Direction},
	}
	if book.ArtifactURL != "" {
		entry.PreviewAction = &Action{Label: "Preview", URL: book.ArtifactURL, ContentType: "audio/wav"}
		entry.ArtifactAction = &Action{Label: "Download WAV", URL: book.ArtifactURL, ContentType: "audio/wav"}
	}
	if status == string(audiobook.ProductionStatusInterrupted) {
		entry.DeleteAction = &Action{Label: "Discard", URL: "/v1/audiobooks/" + book.ID + "/discard", Method: "POST"}
	}
	entries := []Entry{entry}
	for _, render := range book.RenderRevisions {
		renderName := book.Title + " — " + render.ID
		entries = append(entries, Entry{
			Kind: KindRenderRevision, ID: book.ID + "/render/" + render.ID, Name: renderName, Subtype: "wav", CreatedAt: render.CreatedAt,
			Relationship:   &Relationship{Kind: KindAudiobook, ID: book.ID, Name: book.Title},
			Metadata:       map[string]string{"duration_seconds": strconv.Itoa(render.DurationSeconds), "render_id": render.ID},
			PreviewAction:  &Action{Label: "Preview", URL: render.ArtifactURL, ContentType: "audio/wav"},
			ArtifactAction: &Action{Label: "Download WAV", URL: render.ArtifactURL, ContentType: "audio/wav"},
			LaunchAction:   &Action{Label: "Open Audiobook", URL: "/demo/#audiobook"},
			search:         []string{book.Title, book.ID, render.ID, "render revision", "wav"},
		})
	}
	return entries
}

func filterEntries(entries []Entry, query string) []Entry {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return entries
	}
	filtered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if containsSearch(entry.search, query) {
			filtered = append(filtered, entry)
			continue
		}
		children := make([]Entry, 0, len(entry.Children))
		for _, child := range entry.Children {
			if containsSearch(child.search, query) {
				children = append(children, child)
			}
		}
		if len(children) > 0 {
			entry.Children = children
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func containsSearch(values []string, query string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func entryTime(entry Entry) time.Time {
	if !entry.UpdatedAt.IsZero() {
		return entry.UpdatedAt
	}
	return entry.CreatedAt
}

func copyMetadata(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func metadataValues(metadata map[string]string) []string {
	values := make([]string, 0, len(metadata)*2)
	for key, value := range metadata {
		values = append(values, key, value)
	}
	return values
}

func deliveryContentType(format string) string {
	switch format {
	case "mp3":
		return "audio/mpeg"
	case "opus":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	default:
		return "application/octet-stream"
	}
}

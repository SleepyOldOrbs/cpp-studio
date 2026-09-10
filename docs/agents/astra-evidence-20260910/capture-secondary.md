# Secondary current-state capture

Captured 2026-09-10 through mcp__cua_repl only, using an isolated background browser tab at http://127.0.0.1:52510/demo/. No viewport override, real media, model downloads, microphone permissions, or source edits. One image generation used the root-provided fixture runtime; no media quality conclusion is supported. Each saved screenshot was re-read from disk and visually inspected. The browser tab was closed at completion.

## Steps and evidence

- Music & SFX → Music & SFX: 20-music-setup-before.png. Missing-model setup: HeartMuLa selected and disabled, but helper directs user to install ACE-Step (6.19 GB). The install link wraps across two lines in a narrow left column. Audio Seperation navigation is misspelled. Generation disabled.
- Imagery → Image generation: 21-imagery-empty-before.png. Generation selector stays Checking available models; vision reports no compatible model. Header exposes POST /v1/images/generations. Large output placeholder and raw endpoint/engine terms compete with task content.
- Entered synthetic prompt 'A small amber desk lamp on a quiet wooden writing desk.' and clicked Generate image. Fixture result exposed 512x512 PNG, 73 B, seed, Pin, Save PNG, To library and Describe image. 22-imagery-fixture-result-before.png captures result actions and description/playback below. Success despite unavailable selector is fixture-sensitive and needs source corroboration before a product defect claim.
- Stories & audiobooks → Podcast Generator: 23-podcast-setup-before.png. Numbered disclosure steps describe requirements clearly. First view has substantial chrome/model chooser before podcast name/description. Existing defaults include How Stars Are Born and three NASA source excerpts.
- Expanded Provide your sources, focused Excerpt to bring the source fields into view: 24-podcast-sources-before.png. Editable title, URL, excerpt; extra sources collapsed. Source header sits partly underneath sticky global header at this scroll position.
- Clicked Audiobook Builder from the scrolled Podcast source view: 25-audiobook-setup-before.png records exact landing. Route changes but scroll persists, hiding heading/model chooser/document input and landing on lower settings/status disclosures. This is a concrete navigation issue. Manually scrolled to top for 26-audiobook-top-before.png. First-use guidance names accepted formats, file-name title default, and whole-document WAV outcome.
- Clicked Extract: 27-extract-empty-before.png. Blank model field, load button and long technical helper paragraph; large blank waveform. DOM also shows selected result, clean speech mining actor/character fields, training pass-through, and transcript tools before media exists. No upload performed.
- Clicked Open in Transcribe and scrolled top: 28-transcribe-empty-before.png. Load/record actions and disabled transcription controls are visible, then an empty waveform and rename controls. The model selector is blank while helper promises configured Whisper profiles; fixture-sensitive.
- Clicked Training: 29-training-empty-before.png. Empty copy says No speech has been passed from Extract yet, but no direct link/action to Extract. A full-width disabled Export training folder dominates the card. Workspace name Voice LoRA Training implies training, while this state only describes dataset export.

## Cross-surface observations

- Sticky global header/navigation occupies about 240 px of a roughly 712 px usable screenshot height, leaving less than two thirds of screen for workflow content. Nested card/step borders, small uppercase metadata and endpoint badges make screens feel dense despite substantial empty regions.
- The Podcast → Audiobook scroll preservation is stronger behavioral evidence than fixture model failures. Source data and model configuration in this preview are synthetic; verify real-state issues separately.
- Exact screenshots are unaltered viewport images. An initial full-page Music capture was replaced by a valid viewport capture after fullPage produced an oversized canvas; final artifact 20 is the inspected viewport image. A full-page Imagery capture failed, then getScreenshot succeeded.

## Image format note

The browser getScreenshot API returned JPEG bytes despite the requested .png artifact names. Files retain the exact screenshot bytes without conversion and render correctly by content sniffing. No image dimensions were inferred from the filename.

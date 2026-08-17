# bookai — agent notes

## Build / test / lint

A `Taskfile.yml` is provided with all common commands. Run `task --list` to see all tasks.

- Build: `task build` (or `go build ./...`)
- Install: `task install` (or `go install ./...`)
- Tests: `task test` (or `go test ./...`)
- Vet: `task vet` (or `go vet ./...`)
- Lint: `task lint` — runs `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run` (version pinned in Taskfile `GOLANGCI_LINT_VERSION` var)
- Lint + auto-fix: `task lint-fix` — same as `task lint` but with `--fix`
- Check all: `task check` — runs vet + lint + test
- Smoke test: `task smoke` — builds and runs `--help`

Pipeline commands (default project: `books/my-vampire-system-0001-0700`):
- `task status`, `task chapters`, `task verify-glossary` — inspection
- `task analyze`, `task analyze-continue` — glossary/characters extraction
- `task translate`, `task translate-continue` — translation
- `task pronounce`, `task pronounce-continue` — stress marks
- `task ssml`, `task ssml-stress-json` — SSML generation (auto-stress by default, `ssml-stress-json` uses ai/stress.json)
- `task tts` — audio synthesis
- Extra args: `task analyze -- --range 1-50` (passed via `{{.CLI_ARGS}}`)

TTS server (Docker):
- `task tts-up`, `task tts-down`, `task tts-logs`

CI (`.github/workflows/lint.yml`): runs golangci-lint, go vet, and go test on every PR and push to main. golangci-lint version is pinned in the workflow to match `GOLANGCI_LINT_VERSION` in the Taskfile. Uses actions/checkout@v7, actions/setup-go@v7, golangci/golangci-lint-action@v9.

## Architecture

File-based batch pipeline. The filesystem is the database. Every stage reads the previous stage's files and writes its own. Stages are idempotent (skip existing outputs unless `--force`).

Project layout: `cmd/bookai` (binary) + `internal/{epub,chapters,project,config,cli,translation,tts,version}`.

## Decisions (locked)

- Go 1.26, Cobra CLI, YAML config (`gopkg.in/yaml.v3`).
- EPUB parsing: custom reader (`archive/zip` + `encoding/xml` + `golang.org/x/net/html`).
- Translation: OpenAI **official** SDK (`github.com/openai/openai-go`), `gpt-5.6-luna` (default). EN→RU. Uses **Batch API** for cost-effective processing (50% discount). (M2)
- TTS: swappable Engine interface (`internal/tts/engine.go`). Default "noop" engine for pipeline testing. `silero-http` engine for real Silero TTS via Docker server. (M3)
- `bookai import <epub> [name]` auto-creates a project dir (slugified name) in CWD, writes default `config.yaml`, copies EPUB. Other commands use `-p`/`--project` flag (defaults to CWD).
- Chapter classifier: rules-only in M1 (AI fallback designed but not wired).

## Milestone 1 — COMPLETE

EPUB import → chapter detection → JSON artifacts.

Validated on a real 700-chapter EPUB (`books/9kafe.com-my-vampire-system-c1-700.epub`):
- `import`: 702 spine items extracted, OPF at zip root (`book.opf`) handled, NCX parsed correctly.
- `analyze-chapters`: 700 chapters via TOC strategy, 3 sections skipped (Information, TOC, Notes). `--strip` flag (repeatable) removes promotional/boilerplate text from chapter source. Default strip patterns can be set in `config.yaml` under `chapters.strip`; CLI `--strip` flags are appended to config defaults. Strip logic: the trigger pattern is searched in the last 400 chars of each chapter; if found, the `***` separator before the trigger (within the tail only) is located and everything from there to the end is removed. The backward search is limited to the tail to avoid matching scene-break `***` separators in the chapter body. `RawSize` (title + source length before stripping) is stored in `chapter_NNN.json` when strip is applied.
- Idempotent: reruns skip existing artifacts unless `--force` / `--chapter` / `--range`.
- `--strategy toc|heading|per-item` forces a single detection strategy.

## Utility commands

- `bookai status`: quick directory tree with per-stage artifact counts (source, extracted, chapters, ai, translation, memory, tts, audio).
- `bookai chapters`: per-chapter overview table showing raw size (before strip), current size (after strip, with `(!)` for chapters under 1500 bytes), diff (raw - current, with `!` if > 300), glossary terms count, characters count, stress marks count, translation length, SSML status, audio status, and title. Summary line at the bottom shows totals. Uses the `Chapters` field on glossary terms, characters, and stress entries to count per-chapter items. `RawSize` is stored in `chapter_NNN.json` during `analyze-chapters` when strip filters are applied.

## Milestone 2 — Translation core (COMPLETE — Batch API)

- `bookai analyze`: extracts glossary + characters + chapter summaries via **OpenAI Batch API**. Two-phase batch flow: (1) extraction batch — each chapter is a single batch item (JSON mode) that returns glossary/characters AND a chapter summary; (2) merge batch — a single-item batch that deduplicates and classifies all per-chapter results (50% cost discount vs live call). After merge, terms and characters are **tagged with chapter IDs** (`chapters` field) by matching back to per-chapter results — this enables per-chapter glossary filtering in translate. When run on a subset (--chapter/--range), new results are **merged** with existing `ai/glossary.json` + `ai/characters.json` (existing entries + chapter tags are preserved). `--force` starts fresh (ignores existing files). Summaries are saved to `memory/chapter_NNN.summary.txt`. Config `glossary.characters`/`glossary.terms` override AI-extracted translations (locked). Writes `ai/glossary.json` (terms only, with `chapters` field) + `ai/characters.json` (with `chapters` field) + `memory/chapter_NNN.summary.txt`. Extraction batch state persisted in `ai/batch_analyze.json`, merge batch state in `ai/batch_analyze-merge.json`, merge input in `ai/analyze_merge_input.json`. `--continue` checks merge batch first, then extraction batch. Flags: `--force`, `--continue`, `--chapter N`, `--range M-N`, `--poll-interval N` (default 60s).
- `bookai translate`: per-chapter translation via **OpenAI Batch API**. Each chapter is an independent batch item with **per-chapter filtered glossary** (only terms tagged with that chapter ID, plus legacy entries with no chapter tags) + previous 2 chapter summaries (from analyze) as context. This reduces token costs by not sending the full glossary to every chapter. After batch completes, translations are written to disk. No post-processing — summaries and glossary are finalized by analyze. Writes `translation/chapter_NNN.ru.txt`, updates `chapter_NNN.json` status → "translated". Batch state persisted in `ai/batch_translate.json` for `--continue` resume. Flags: `--force`, `--continue`, `--chapter N`, `--range M-N`, `--skip-memory`, `--poll-interval N` (default 60s).
- `bookai verify-glossary`: scans translations for untranslated English glossary terms, writes `ai/glossary_violations.json`.
- `bookai pronounce`: scans each chapter's translation via **OpenAI Batch API** for words with non-obvious stress. One batch item per chapter — the model sees the full chapter text and returns {term, stressed} pairs using Silero stress marks (`+` before the stressed vowel, e.g., `кедров` → `к+едров`). Per-chapter results are merged into global `ai/stress.json` via `MergeChapter`, which tags each entry with the chapter ID (`chapters` field) for per-chapter tracking. Conflicts (same term, different stress) resolved interactively. Config `pronunciation` overrides are passed to the model. Batch state persisted in `ai/batch_pronounce.json`. Flags: `--force` (re-process chapters, merge with existing vocabulary), `--continue`, `--reset` (wipe `ai/stress.json` before processing, backs up to `stress.json.bak` which is restored on failure or deleted on success), `--chapter N`, `--range M-N`, `--poll-interval N` (default 60s).
- OpenAI SDK: official `github.com/openai/openai-go/v3` (v3.47.0). Uses the **Responses API** (`client.Responses.New`) for live calls and **Batch API** (`client.Batches.New`) for bulk processing. System prompt → `instructions` param, user message → `input` param, JSON mode → `text.format = json_object`. Built-in retry via `option.WithMaxRetries`.
- Batch API flow: build JSONL → upload file (purpose `batch`) → create batch (endpoint `/v1/responses`, 24h window) → poll every N seconds → download output file → parse results. `internal/translation/batch.go` handles all batch operations. `internal/translation/batch_state.go` persists state locally.
- Default model: `gpt-5.6-luna` for both translation and helper tasks (configurable in `config.yaml`).

## Milestone 3 — TTS pipeline (COMPLETE — Silero TTS with SSML and stress marks)

- `bookai ssml`: converts `translation/chapter_NNN.ru.txt` into `tts/chapter_NNN.ssml` (SSML with stress marks applied). By default, the silero-stress model on the TTS server is used — text is split into sentences and sent to `POST /api/stress`, which returns each sentence with `+` marks before stressed vowels (handles homograph disambiguation within sentence context). No `stress.json` needed. With `--use-stress-json`, stress marks from the global `ai/stress.json` (built by `bookai pronounce`) are applied instead via greedy longest-match, case-insensitive, word-boundary aware text replacement. Config `pronunciation` overrides are always applied on top of either stress source. Text is then wrapped in SSML tags (`<speak>`, `<p>`, `<s>`) for Silero TTS. Before processing, scans all chapters for Latin words (untranslated/fused terms from the translation model) — if found, lists them and prompts the user to transliterate (best-effort Latin→Cyrillic) or abort. Flags: `--force`, `--chapter N`, `--range M-N`, `--use-stress-json` (uses ai/stress.json instead of auto-stress).
- `bookai tts`: synthesizes `tts/chapter_NNN.ssml` into `audio/chapter_NNN.mp3` (128kbps mono) via the configured engine. Engines produce WAV internally; CLI converts to MP3 via ffmpeg. Hard-fails if SSML contains any Latin letters or bad symbols (non-Cyrillic, non-Latin) — the SSML stage should have caught and transliterated/stripped these. After synthesis, queries the Silero Docker container logs (`docker logs --since <start_time> bookai-silero`) for `[WARN]`/`[ERROR]` entries, matches text snippets to chapter SSML files, and reports any chapters that had server-side warnings (e.g. SSML parsing failures that produce silence). Best-effort: silently skips if Docker is unavailable. Config: `tts.audio_format` ("mp3" default, "wav" fallback), `tts.audio_bitrate` ("128k" default), `tts.parallel` (concurrent chunk requests per chapter, default 1).
- Engine interface: `tts.Engine` with `Name()` and `Synthesize(ctx, text, outPath)`. Engines register via `tts.Register(name, factory)`. `tts.NewEngine(cfg)` looks up the factory.
- NoopEngine: default engine that writes a valid sine-tone WAV. Requires no external deps — enables full pipeline testing (ssml → audio) without a real TTS backend.
- SileroEngine (`silero-http`): real TTS via a remote Silero TTS server (biblio-tts-server-silero). Sends SSML text to `POST /api/tts` with `ssml: true`, writes the returned WAV. Long chapters are split into chunks under 900 chars; with `tts.parallel > 1`, chunks are synthesized concurrently via a worker pool and concatenated in order. Config: `tts.server_url`, `tts.voice` (e.g., `silero:v5_5_ru#xenia`), `tts.parallel` (default 1). 10-minute timeout for long chapters + first model load.
- Stress marks system: `internal/tts/stress.go` — `Stress` store (ai/stress.json global) maps terms to stressed forms with `+` before the stressed vowel. `StressEntry` has a `Chapters []int` field tracking which chapters produced each entry. `Stress.Apply(text)` replaces terms in text. `MergeChapter(other, chID)` merges per-chapter results with conflict detection and chapter tagging. `CountForChapter(chID)` returns the number of entries for a chapter. Resolved conflicts are marked `Approved` and silently kept on future runs — no re-asking.
- SSML generation: `internal/tts/ssml.go` — `GenerateSSML(text)` splits text into paragraphs and sentences, wraps in `<speak>`/`<p>`/`<s>` tags. Stress marks preserved as-is. Malformed stress marks (`+` not before a Cyrillic vowel) are stripped via `sanitizeStressMarks()`. Latin characters are replaced with Cyrillic look-alikes via `latinToCyrillic()` to prevent Silero parser crashes.
- Config: `tts.engine` ("noop" default, "silero-http" for real TTS), `tts.language`, `tts.server_url`, `tts.voice`, `tts.speed`, `tts.pitch`, `tts.sample_rate`, `tts.audio_format` ("mp3" default, "wav"), `tts.audio_bitrate` ("128k" default), `tts.parallel` (concurrent chunk requests, default 1).
- Flags: `--force`, `--chapter N`, `--range M-N`.
- TTS server: `tts-server/` directory with `docker-compose.yml` using prebuilt `vpoluyaktov/bibliohub-tts-server-silero:dev-latest` image + custom `server.py` entry point that runs multiple uvicorn workers for parallel request processing. Each worker has its own model copy and `torch.set_num_threads(1)` to avoid CPU oversubscription. `SILERO_WORKERS` env var controls worker count (default 4). The server also exposes `POST /api/stress` (silero-stress model) for automatic stress placement — used by `bookai ssml` (default auto-stress mode). The stress endpoint is mounted via `biblio_stress_app.py` wrapper, `stress_endpoint.py` router, and `entrypoint.sh` installs `silero-stress` on first start. See `tts-server/README.md`.
- To add a real engine: create `internal/tts/<engine>.go`, implement `Engine`, add a `Register("name", factory)` call inside `RegisterEngines()` in `internal/tts/engine.go`. No changes needed to CLI or pipeline code. The registry self-initializes via `sync.Once` so callers outside the bookai binary (tests, future entry points) never observe an empty registry.

## Conventions

- Errors: wrap with `%w`, surface a single clean line to the user; full chain only with `--verbose` (see `cli.Fail`).
- JSON artifacts: 2-space indent + trailing newline for stable diffs (see `project.SaveJSON`).
- Secrets: `OPENAI_API_KEY` from env only, never in `config.yaml`.
- Context: every stage takes `context.Context`; CLI wires a `signal.NotifyContext` root.
- Doc comments on exported symbols must start with the symbol name (revive rule).
- Logging: use `slog.Default()` for all diagnostic/report output (Info, Warn, Error). Do NOT use `fmt.Fprintf(os.Stdout, ...)` for reports or warnings. The only exceptions are: (1) interactive prompts that read user input on the same terminal (e.g. "Choose: " in ssml/pronounce), and (2) display commands whose output is a formatted table/tree meant to be read as-is (e.g. `bookai chapters`, `bookai status`, `--version`).

## Next

Potential future work:
- Voice management: CLI command to list/preview Silero voices on the server.
- Streaming synthesis: chunk long chapters to avoid timeouts and show progress.
- **Character→voice mapping (postponed)**: see "Future: Character→voice mapping" below.

## Future: Character→voice mapping (postponed)

**Goal**: assign a distinct TTS voice to each character in the book, so dialogue
is spoken in the character's voice and narration in the default/narrator voice.

**Why postponed**: the translated text (`translation/chapter_NNN.ru.txt`) has no
speaker markers. Switching voices per character during synthesis requires
dialogue detection (heuristic quote/«...» detection + name-mention matching),
which adds significant complexity. The feature was scoped but not implemented.

**Design notes** (from discussion, ready to pick up):

1. **Config section** — optional `tts.character_voices` map in `config.yaml`:
   ```yaml
   tts:
     engine: silero-http
     voice: silero:v5_5_ru#xenia  # default/narrator voice
     character_voices:
       "Джон": silero:v5_5_ru#aidar
       "Мэри": silero:v5_5_ru#baya
   ```
   If a character isn't in the map, the default `voice` is used.

2. **New command: `bookai assign-voices`** — uses AI to match characters to
   voices. Reads `ai/characters.json` (already has name, role, description
   from `bookai analyze`), fetches the voice list from the TTS server
   (`GET /api/voices`), and asks the helper model to pick the best voice for
   each character. Writes the mapping to `ai/voice_mapping.json` (or directly
   into `config.yaml`).

3. **Voice characteristics** — Silero Russian voices are: aidar (M), baya (F),
   kseniya (F), eugene (M), xenia (F). Voice IDs are `silero:{model}#{speaker}`.
   Options for describing voices to the AI:
   - **Derive from speaker name**: pass "aidar" → let AI infer gender from
     the name. No extra files needed.
   - **Pre-generated `voices.json`**: a metadata file with a short description
     per voice. Reusable across all books.
   - **AI describes on-the-fly**: during `assign-voices`, ask the model to
     describe each voice based on its name, then match.

4. **Dialogue detection for TTS** — three approaches considered:
   - **Full multi-voice**: detect which character speaks each quoted line
     (match by name mention in the surrounding paragraph). Most complex.
   - **Narrator vs dialogue split**: narrator gets default voice, any quoted
     text gets a character voice (matched by name in paragraph). Simpler.
   - **Postponed entirely**: just build the config + `assign-voices` command
     now, use one voice for all text. Add dialogue detection later.

5. **Silero server changes** — the current `POST /api/tts` endpoint takes one
   `voice` parameter. For multi-voice synthesis, either:
   - Client-side: split text into segments, call `/api/tts` per segment with the
     appropriate voice, concatenate WAVs. Simple but slower (more requests).
   - Server-side: add a `/api/tts-multi` endpoint that accepts segments with
     per-segment voice assignments. Faster, single inference pass.

**Prerequisites before implementing**:
- `ai/characters.json` must exist (run `bookai analyze` first).
- TTS server must be running (for `GET /api/voices`).
- Decide on dialogue detection approach (full vs narrator-split vs none).

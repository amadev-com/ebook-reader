# bookai — agent notes

## Build / test / lint

- Build: `go build ./...`
- Tests: `go test ./...`
- Vet: `go vet ./...`
- Lint: `golangci-lint run` (v2.12.2, built with Go 1.26 — works)
- Smoke test the binary: `go build -o /tmp/bookai ./cmd/bookai && /tmp/bookai --help`

## Architecture

File-based batch pipeline. The filesystem is the database. Every stage reads the previous stage's files and writes its own. Stages are idempotent (skip existing outputs unless `--force`).

Project layout: `cmd/bookai` (binary) + `internal/{epub,chapters,project,config,cli,translation,tts,version}`.

## Decisions (locked)

- Go 1.26, Cobra CLI, YAML config (`gopkg.in/yaml.v3`).
- EPUB parsing: custom reader (`archive/zip` + `encoding/xml` + `golang.org/x/net/html`).
- Translation: OpenAI **official** SDK (`github.com/openai/openai-go`), `gpt-4.1` / `gpt-4.1-mini`. EN→RU. (M2)
- TTS: swappable Engine interface (`internal/tts/engine.go`). Default "noop" engine for pipeline testing. `xtts-http` engine for real XTTS v2 via Docker server. (M3)
- `bookai import <epub> [name]` auto-creates a project dir (slugified name) in CWD, writes default `config.yaml`, copies EPUB. Other commands use `-p`/`--project` flag (defaults to CWD).
- Chapter classifier: rules-only in M1 (AI fallback designed but not wired).

## Milestone 1 — COMPLETE

EPUB import → chapter detection → JSON artifacts.

Validated on a real 700-chapter EPUB (`books/9kafe.com-my-vampire-system-c1-700.epub`):
- `import`: 702 spine items extracted, OPF at zip root (`book.opf`) handled, NCX parsed correctly.
- `analyze-chapters`: 700 chapters via TOC strategy, 3 sections skipped (Information, TOC, Notes).
- Idempotent: reruns skip existing artifacts unless `--force` / `--chapter` / `--range`.
- `--strategy toc|heading|per-item` forces a single detection strategy.

## Milestone 2 — Translation core (COMPLETE)

- `bookai analyze`: extracts glossary + characters from chapter snippets via gpt-4.1-mini (JSON mode). Writes `ai/glossary.json` + `ai/characters.json`.
- `bookai translate`: per-chapter translation via gpt-4.1 with glossary + previous 2 chapter summaries as context. Three-step loop: translate → summarize → extract new terms. Writes `translation/chapter_NNN.ru.txt`, `memory/chapter_NNN.summary.txt`, updates `chapter_NNN.json` status → "translated", merges new terms into glossary.
- `bookai verify-glossary`: scans translations for untranslated English glossary terms, writes `ai/glossary_violations.json`.
- OpenAI SDK: official `github.com/openai/openai-go/v3` (v3.47.0). Built-in retry via `option.WithMaxRetries`.
- Flags: `--force`, `--chapter N`, `--range M-N`, `--skip-memory`, `--skip-glossary-update`.

## Milestone 3 — TTS pipeline (COMPLETE — scaffolding with swappable engine)

- `bookai ssml`: converts `translation/chapter_NNN.ru.txt` into `tts/chapter_NNN.ssml` (W3C SSML with `<p>`/`<s>`/`<phoneme>` tags). Pronunciation hints from `ai/pronunciation.json` applied via greedy longest-match, case-insensitive, word-boundary aware.
- `bookai tts`: synthesizes `tts/chapter_NNN.ssml` into `audio/chapter_NNN.wav` via the configured engine (`tts.engine` in config.yaml). `--merge` concatenates all chapter WAVs into `audio/book.wav` via ffmpeg.
- Engine interface: `tts.Engine` with `Name()` and `Synthesize(ctx, ssml, outPath)`. Engines register via `tts.Register(name, factory)`. `tts.NewEngine(cfg)` looks up the factory.
- NoopEngine: default engine that writes a valid sine-tone WAV. Requires no external deps — enables full pipeline testing (SSML → audio → merge) without a real TTS backend.
- `tts.ExtractPlainText(ssml)`: strips SSML tags for engines that don't support SSML natively.
- Config: `tts.engine` ("noop" default), `tts.language`, `tts.model_path`, `tts.data_dir`, `tts.tokens_path`, `tts.device`, `tts.speed`, `tts.voice_sample`, `tts.python`.
- Flags: `--force`, `--chapter N`, `--range M-N`, `--merge`.
- Validated end-to-end: import → analyze-chapters → (fake translation) → ssml → tts → merge. Idempotent, `--force` works, pronunciation hints apply correctly.
- To add a real engine: create `internal/tts/<engine>.go`, implement `Engine`, call `Register("name", factory)` in `init()`. No changes needed to CLI or pipeline code.

## Conventions

- Errors: wrap with `%w`, surface a single clean line to the user; full chain only with `--verbose` (see `cli.Fail`).
- JSON artifacts: 2-space indent + trailing newline for stable diffs (see `project.SaveJSON`).
- Secrets: `OPENAI_API_KEY` from env only, never in `config.yaml`.
- Context: every stage takes `context.Context`; CLI wires a `signal.NotifyContext` root.
- Doc comments on exported symbols must start with the symbol name (revive rule).

## Milestone 3 — COMPLETE (TTS pipeline + XTTS v2 HTTP engine)

- `bookai ssml`: converts `translation/chapter_NNN.ru.txt` into `tts/chapter_NNN.ssml` (W3C SSML with `<p>`/`<s>`/`<phoneme>` tags). Pronunciation hints from `ai/pronunciation.json` applied via greedy longest-match, case-insensitive, word-boundary aware.
- `bookai tts`: synthesizes `tts/chapter_NNN.ssml` into `audio/chapter_NNN.wav` via the configured engine (`tts.engine` in config.yaml). `--merge` concatenates all chapter WAVs into `audio/book.wav` via ffmpeg.
- Engine interface: `tts.Engine` with `Name()` and `Synthesize(ctx, ssml, outPath)`. Engines register via `tts.Register(name, factory)`. `tts.NewEngine(cfg)` looks up the factory.
- NoopEngine: default engine that writes a valid sine-tone WAV. Requires no external deps — enables full pipeline testing (SSML → audio → merge) without a real TTS backend.
- HTTPEngine (`xtts-http`): real TTS via a remote XTTS v2 FastAPI server. Sends plain text (SSML stripped via `ExtractPlainText`) to `POST /tts`, writes the returned WAV. Config: `tts.server_url`, `tts.speaker` (or `tts.voice_sample` for absolute path). 10-minute timeout for long chapters + first model load.
- `tts.ExtractPlainText(ssml)`: strips SSML tags for engines that don't support SSML natively.
- Config: `tts.engine` ("noop" default, "xtts-http" for real TTS), `tts.language`, `tts.server_url`, `tts.speaker`, `tts.voice_sample`, `tts.speed`, `tts.model_path`, `tts.data_dir`, `tts.tokens_path`, `tts.device`, `tts.python`.
- Flags: `--force`, `--chapter N`, `--range M-N`, `--merge`.
- TTS server: `tts-server/` directory with `docker-compose.yml` + `server.py`. Uses `athomasson2/ebook2audiobook:cu130` image (CUDA 13.0 + PyTorch 2.11 + coqui-tts 0.27.5) for Blackwell GPU support. See `tts-server/README.md`.
- Validated end-to-end with real XTTS v2: SSML → HTTP engine → 24kHz Russian WAV. Model downloads on first call (~1.8GB, cached in `tts-server/models/`).

## Next

Potential future work:
- Pronunciation hints: currently SSML-only. Could send phoneme hints to the XTTS server.
- Voice sample management: CLI command to list/add/preview voices on the server.
- Streaming synthesis: chunk long chapters to avoid timeouts and show progress.
- Fine-tuned models: mount a custom XTTS v2 model via `tts-server/models/`.

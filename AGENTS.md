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
- TTS: XTTS v2 (Python subprocess) — M3 only.
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
- `ssml`/`tts` remain stubs (M3).

## Conventions

- Errors: wrap with `%w`, surface a single clean line to the user; full chain only with `--verbose` (see `cli.Fail`).
- JSON artifacts: 2-space indent + trailing newline for stable diffs (see `project.SaveJSON`).
- Secrets: `OPENAI_API_KEY` from env only, never in `config.yaml`.
- Context: every stage takes `context.Context`; CLI wires a `signal.NotifyContext` root.
- Doc comments on exported symbols must start with the symbol name (revive rule).

## Next: Milestone 3 (TTS)

See `/home/max/.devin/plans/plan-2125d2a83ef7b1fc.md` Phase 5. Key artifacts ready:
- `translation/chapter_NNN.ru.txt` (from M2) feeds SSML generation.
- `ai/pronunciation.json` (to be populated during M3 glossary extraction).
- `tts/` and `audio/` dirs ready.
- Config has `tts.engine`, `tts.voice_sample`, `tts.language`, `tts.python` defaults.

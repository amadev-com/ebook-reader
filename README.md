# bookai

A local, file-based batch pipeline that turns an EPUB into a consistently-translated, glossary-backed audiobook.

## How it works

Each book lives in its own **project directory**. The filesystem is the database — every stage reads the previous stage's files and writes its own. Stages are idempotent: rerunning skips existing artifacts unless `--force` is passed.

```
source/original.epub
        │  1. bookai import
        ▼
extracted/   (spine.json, toc.json, blocks/*.json)
        │  2. bookai analyze-chapters
        ▼
chapters/    (chapter_NNN.json, _skipped.json, _index.json)
        │  3. bookai analyze            ← requires OPENAI_API_KEY
        ▼
ai/          (glossary.json, characters.json)
        │  4. bookai translate          ← requires OPENAI_API_KEY
        ▼
translation/ + memory/   (chapter_NNN.ru.txt, chapter_NNN.summary.txt)
        │  5. bookai verify-glossary    ← optional QA check
        ▼
        │  6. bookai pronounce          ← optional, requires OPENAI_API_KEY (Batch API)
        ▼
ai/          (stress.json)
        │  7. bookai ssml               ← auto-stress by default (TTS server) or --use-stress-json
        ▼
tts/         (chapter_NNN.ssml)
        │  8. bookai tts                ← requires Silero TTS server (Docker)
        ▼
audio/       (chapter_NNN.mp3)
```

## Prerequisites

### 1. Build the CLI

```bash
go build -o bookai ./cmd/bookai
```

Or install to `$GOPATH/bin`:

```bash
go install ./...
```

### 2. Set up the OpenAI API key

Translation and glossary extraction use OpenAI's API:

```bash
export OPENAI_API_KEY="sk-..."
```

The key is read from the environment only — it is never stored in `config.yaml`.

### 3. Start the TTS server (one-time, Docker)

The Silero TTS server runs in Docker and provides SSML and stress mark support for high-quality Russian speech synthesis. It stays running across books — start it once.

```bash
cd tts-server
docker compose up -d
```

The server listens on `http://localhost:5555`. Models are preloaded at startup. List available voices with:

```bash
curl -s http://localhost:5555/api/voices?language=ru | python3 -m json.tool
```

See [`tts-server/README.md`](tts-server/README.md) for full details.

> **No GPU?** Set `tts.engine: noop` in `config.yaml` to generate placeholder sine-tone WAVs for pipeline testing without a TTS server.

## Taskfile shortcuts

A `Taskfile.yml` is provided with all common commands. [Install task](https://taskfile.dev/installation/) if you don't have it, then run `task --list` to see all available tasks.

**Development:**

| Task | Description |
|------|-------------|
| `task build` | Build the bookai binary |
| `task install` | Install to `$GOPATH/bin` |
| `task test` | Run all tests |
| `task vet` | Run `go vet` |
| `task lint` | Run golangci-lint (version pinned via `GOLANGCI_LINT_VERSION` var) |
| `task lint-fix` | Run golangci-lint with `--fix` |
| `task check` | Run vet + lint + test |
| `task smoke` | Build and run `--help` to verify the binary |

**TTS server (Docker):**

| Task | Description |
|------|-------------|
| `task tts-up` | Start the Silero TTS server |
| `task tts-down` | Stop the TTS server |
| `task tts-logs` | Tail server logs |

**Pipeline** (default project: `books/my-vampire-system-0001-0700`):

| Task | Description |
|------|-------------|
| `task status` | Show project status |
| `task chapters` | Per-chapter overview table |
| `task analyze` | Extract glossary + characters (Batch API) |
| `task analyze-continue` | Resume interrupted analyze batch |
| `task translate` | Translate chapters (Batch API) |
| `task translate-continue` | Resume interrupted translate batch |
| `task verify-glossary` | Scan for untranslated terms |
| `task pronounce` | Generate stress marks (Batch API) |
| `task pronounce-continue` | Resume interrupted pronounce batch |
| `task ssml` | Generate SSML from translations (auto-stress by default) |
| `task ssml-stress-json` | Generate SSML using ai/stress.json instead of auto-stress |
| `task tts` | Synthesize audio from SSML |

Pass extra args to pipeline tasks with `--`:

```bash
task analyze -- --range 1-50
task translate -- --chapter 42
```

## Processing a book: step by step

### Step 1 — Import the EPUB

```bash
bookai import /path/to/book.epub
```

This creates a project directory named after the EPUB file (lowercase, dashes), copies the book inside, writes a default `config.yaml`, and extracts the spine, TOC, and per-item text blocks.

For example, `my-vampire-system.epub` creates `./my-vampire-system/`. To use a custom name:

```bash
bookai import /path/to/book.epub "My Vampire System"   # creates ./my-vampire-system/
```

To import into an explicit directory (skips auto-creation):

```bash
bookai import /path/to/book.epub -p /existing/project-dir
```

> **Before continuing:** edit `my-vampire-system/config.yaml` to switch the TTS engine from `noop` to `silero-http` and pick a voice (see [Configuration](#configuration) below).

### Step 2 — Detect chapters

```bash
bookai analyze-chapters -p my-vampire-system
```

Writes `chapters/chapter_NNN.json` (one per chapter), `chapters/_index.json`, and `chapters/_skipped.json` (non-chapter sections like TOC/notes).

If chapters contain promotional text, author notes, or other boilerplate after a `***` separator, strip them with `--strip` (repeatable):

```bash
bookai analyze-chapters -p my-vampire-system --force \
  --strip "For MVS artwork" \
  --strip "Want another mass release" \
  --strip "We hit 22,000 Stones"
```

Each `--strip` string is a **trigger**: if it appears in the last 400 chars of a chapter, everything from the last `***` separator before the trigger to the end of the chapter is removed. This handles variations in the boilerplate text — you only need to match a short unique fragment.

To avoid passing `--strip` every time, set default strip patterns in `config.yaml` (see [Configuration](#configuration) below):

```yaml
chapters:
  strip:
    - "For MVS artwork"
    - "Want another mass release"
    - "We hit 22,000 Stones"
```

CLI `--strip` flags are appended to the config defaults.

### Step 3 — Extract glossary and characters

```bash
bookai analyze -p my-vampire-system
```

Uses the **OpenAI Batch API** for cost-effective processing (50% discount). Each chapter is a single batch item that returns both the glossary/character extraction AND a chapter summary in one JSON response — the model reads each chapter once instead of twice. After the batch completes, a single "live" merge/unify request deduplicates and classifies all results: **characters** = only real persons from the story, **glossary** = terms (places, organizations, titles) without any character entries. Writes `ai/glossary.json`, `ai/characters.json`, and `memory/chapter_NNN.summary.txt` (summaries used as context by translate).

The command stays in polling mode, checking batch status every 60 seconds. If interrupted, use `--continue` to resume:

```bash
bookai analyze -p my-vampire-system --continue
```

Options:
- `--continue` — resume polling an interrupted batch
- `--chapter N` / `--range M-N` — analyze only a subset of chapters
- `--poll-interval N` — seconds between status polls (default 60)
- `--force` — re-analyze even if glossary already exists

```bash
bookai analyze -p my-vampire-system --range 1-100
```

### Step 4 — Translate

```bash
bookai translate -p my-vampire-system
```

Uses the **OpenAI Batch API** for cost-effective processing. Each chapter is submitted as an independent batch item with the glossary and previous chapter summaries (from analyze) as context. After the batch completes, translations are written to `translation/chapter_NNN.ru.txt`. No post-processing — summaries and glossary are finalized by analyze. Updates chapter status to `translated`.

The command stays in polling mode. If interrupted, use `--continue` to resume:

```bash
bookai translate -p my-vampire-system --continue
```

Options:
- `--continue` — resume polling an interrupted batch
- `--chapter N` / `--range M-N` — translate only a subset of chapters
- `--skip-memory` — don't load previous chapter summaries as context
- `--poll-interval N` — seconds between status polls (default 60)
- `--force` — re-translate chapters whose translation already exists

```bash
bookai translate -p my-vampire-system --range 1-50
bookai translate -p my-vampire-system --range 51-100
```

### Step 5 — Verify glossary (optional QA)

```bash
bookai verify-glossary -p my-vampire-system
```

Scans translations for untranslated English glossary terms. Writes `ai/glossary_violations.json`.

### Step 6 — Generate stress marks (optional)

```bash
bookai pronounce -p my-vampire-system
```

> **Note:** This step is optional. By default, `bookai ssml` uses the silero-stress model on the TTS server for automatic stress placement. Only run `pronounce` if you want to use `--use-stress-json` with `bookai ssml`, or if you want to review and lock stress marks via the OpenAI Batch API.

Scans each chapter's translation via the **OpenAI Batch API** for words with non-obvious or ambiguous stress. One batch item per chapter — the model sees the full chapter text and returns a list of {term, stressed} pairs using the Silero stress mark convention: a `+` before the stressed vowel (e.g., `кедров` → `к+едров`). Per-chapter results are saved to `ai/stress_NNN.json`, then merged into a single global `ai/stress.json` vocabulary. If the same term has different stress marks across chapters, you'll be prompted to resolve the conflict interactively. Config `pronunciation` overrides are passed to the model. Flags: `--force`, `--continue`, `--reset`, `--range M-N`, `--poll-interval N`.

- `--reset` — erase existing `ai/stress.json` and rebuild from scratch (without it, new results are merged with the existing file)

### Step 7 — Generate SSML

```bash
bookai ssml -p my-vampire-system
```

Converts each `translation/chapter_NNN.ru.txt` into `tts/chapter_NNN.ssml` (SSML with stress marks applied). By default, the **silero-stress model** on the TTS server applies stress marks — text is split into sentences and sent to `POST /api/stress`, which handles homograph disambiguation within sentence context. No `pronounce` step needed. Config `pronunciation` overrides are always applied on top. The text is then wrapped in SSML tags (`<speak>`, `<p>`, `<s>`) for Silero TTS. Requires `tts.server_url` in config and the TTS server to be running.

**Using ai/stress.json instead:** With `--use-stress-json`, stress marks from the global `ai/stress.json` (built by `bookai pronounce`) are applied via greedy longest-match text replacement instead of the silero-stress model. This is local processing — no network needed beyond loading the file.

```bash
bookai ssml -p my-vampire-system --use-stress-json    # use ai/stress.json
```

**Latin word detection:** Before processing, the command scans all chapters for Latin words or bad symbols (non-Cyrillic, non-Latin letters like CJK) that the translation model may have left behind (e.g. "infusedирована", "Boneclaw"). If any are found, it lists them per chapter and prompts:

```
Found problematic text in 2 chapter(s) — Silero TTS cannot handle these:
  Chapter 557: Latin words: infusedирована
  Chapter 659: bad symbols: 忙

Options:
  [1] Transliterate Latin words and strip bad symbols, then proceed
  [2] Abort (fix translations first)
```

Option [1] transliterates Latin words to Cyrillic (visual look-alikes + phonetic equivalents; all-uppercase acronyms are spelled out letter-by-letter) and strips bad symbols. Option [2] aborts so you can fix the translations first.

Flags: `--force`, `--chapter N`, `--range M-N`, `--use-stress-json`.

### Step 8 — Synthesize audio

```bash
bookai tts -p my-vampire-system
```

Synthesizes each `tts/chapter_NNN.ssml` into `audio/chapter_NNN.mp3` (128kbps mono) via the Silero TTS server. The engine sends SSML text with `ssml: true` to the server's `POST /api/tts` endpoint. Long chapters are split into chunks under 900 characters; with `tts.parallel > 1` (default 1), chunks are synthesized concurrently and concatenated in order. Engines produce WAV internally; the CLI converts to MP3 via ffmpeg. To output WAV instead, set `tts.audio_format: wav` in `config.yaml`.

**Pre-synthesis check:** Hard-fails if SSML text content contains Latin letters or bad symbols (non-Cyrillic, non-Latin) — Silero cannot handle them and would crash or produce silence. The SSML stage should have caught these; if you hit this error, run `bookai ssml --force` to regenerate with transliteration/stripping.

**Post-synthesis verification:** After all chapters are synthesized, the command queries the Silero Docker container logs for `[WARN]`/`[ERROR]` entries generated during the run, matches them to chapters by text snippets, and reports any chapters that had server-side warnings (e.g. SSML parsing failures that produce silence instead of audio). Best-effort: silently skips if Docker is unavailable.

### Check progress at any time

```bash
bookai status -p my-vampire-system
```

Shows a directory tree and per-stage artifact counts.

For a detailed per-chapter overview:

```bash
bookai chapters -p my-vampire-system
```

Shows a table with raw size (before strip), current size (after strip), diff, glossary terms, characters, stress marks, translation length, SSML, and audio status for every chapter. Chapters under 1500 bytes are marked with `(!)`. Diffs over 300 bytes are marked with `!`.

> **Tip:** If you `cd` into the project directory, you can omit `-p` from all commands:
> ```bash
> cd my-vampire-system
> bookai analyze-chapters
> bookai analyze
> bookai translate
> bookai tts
> ```

## Working with multiple books

Each book is an independent project directory, auto-created by `import`. To process another book, just run import again from the same parent directory:

```bash
cd ~/books
bookai import /path/to/first-book.epub       # creates ~/books/first-book/
bookai import /path/to/second-book.epub      # creates ~/books/second-book/
```

Then run the remaining stages for each, pointing `-p` at the project directory:

```bash
bookai analyze-chapters -p first-book
bookai translate -p first-book
bookai tts -p first-book

bookai analyze-chapters -p second-book
bookai translate -p second-book
bookai tts -p second-book
```

The TTS server (`tts-server/`) and `OPENAI_API_KEY` are shared across all books.

### Cleaning up a project

To start over or remove a book entirely, delete its project directory:

```bash
rm -rf ~/books/my-vampire-system
```

To redo a specific stage without nuking everything, use `--force` on the relevant command. This regenerates that stage's artifacts (and downstream stages will pick them up on the next run):

```bash
bookai translate --chapter 5 --force    # re-translate chapter 5 only
bookai ssml --force                     # regenerate all SSML
bookai tts --force                      # re-synthesize all audio
```

To selectively clean a stage, remove its output directory and rerun:

```bash
rm -rf audio/ && bookai tts          # re-synthesize all audio
rm -rf tts/ && bookai ssml           # regenerate all SSML
```

> **Note:** `ai/glossary.json` accumulates terms across chapters during translation. Deleting it and rerunning `bookai analyze` + `bookai translate --force` will rebuild it from scratch, but you'll lose any manual edits.

## Flags

All stage commands support:

| Flag | Description |
|------|-------------|
| `--force` | Regenerate artifacts even if they already exist |
| `--continue` | Resume polling an interrupted batch (analyze, translate) |
| `--chapter N` | Process a single chapter (1-based) |
| `--range M-N` | Process a range of chapters |
| `--poll-interval N` | Seconds between batch status polls (default 60, analyze/translate) |
| `--skip-memory` | Don't load previous chapter summaries as context (translate) |
| `--project PATH` | Path to the project directory (default: current dir) |
| `--verbose` | Enable debug logging |

## Configuration

`bookai import` auto-creates a `config.yaml` with defaults in the project directory. Edit it to customize the TTS engine, voice, or models:

```yaml
project: my-vampire-system
languages:
  source: en
  target: ru
openai:
  translation_model: gpt-5.6-luna    # model for translation (Batch API)
  helper_model: gpt-5.6-luna         # model for glossary/summary/merge (Batch API)
  max_retries: 3
chapters:                         # optional: default --strip patterns for analyze-chapters
  strip:
    - "For MVS artwork"
    - "Want another mass release"
tts:
  engine: silero-http            # "noop" (default) or "silero-http"
  language: ru
  server_url: http://localhost:5555
  voice: silero:v5_5_ru#xenia    # Silero voice ID (model#speaker)
  sample_rate: 48000             # output sample rate
  speed: 1.0                     # playback speed (1.0 = normal)
  pitch: 1.0                     # pitch multiplier (1.0 = normal)
  audio_format: mp3              # "mp3" (default) or "wav"
  audio_bitrate: "128k"          # MP3 bitrate (default 128k)
  parallel: 4                    # concurrent API requests per chapter (default 1)
glossary:                        # optional: lock specific translations
  characters:
    - source: Quinn
      target: Куинн
    - source: Fex
      target: Фекс
  terms:
    - source: The Order
      target: Орден
      type: organization
    - source: Dalki
      target: Далки
      type: term
pronunciation:                   # optional: lock stress marks for Silero
  - term: Куинн
    phonemes: К+уинн              # + before stressed vowel
  - term: Далки
    phonemes: Д+алки
```

The OpenAI API key is read from the `OPENAI_API_KEY` environment variable — it is **never** stored in `config.yaml`.

## Build

```bash
go build ./cmd/bookai
```

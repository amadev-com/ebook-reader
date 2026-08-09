# bookai

A local, file-based batch pipeline that turns an EPUB into a consistently-translated, glossary-backed audiobook.

> **Status: Milestone 3 complete.** EPUB import, chapter detection, glossary extraction, translation, SSML generation with stress marks, and Silero text-to-speech are implemented.

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
        │  6. bookai pronounce          ← requires OPENAI_API_KEY (Batch API)
        ▼
ai/          (stress_NNN.json per chapter → merged into stress.json)
        │  7. bookai ssml               ← local: apply stress marks + wrap in SSML
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

### Step 6 — Generate stress marks

```bash
bookai pronounce -p my-vampire-system
```

Scans each chapter's translation via the **OpenAI Batch API** for words with non-obvious or ambiguous stress. One batch item per chapter — the model sees the full chapter text and returns a list of {term, stressed} pairs using the Silero stress mark convention: a `+` before the stressed vowel (e.g., `кедров` → `к+едров`). Per-chapter results are saved to `ai/stress_NNN.json`, then merged into a single global `ai/stress.json` vocabulary. If the same term has different stress marks across chapters, you'll be prompted to resolve the conflict interactively. Config `pronunciation` overrides are passed to the model. Flags: `--force`, `--continue`, `--reset`, `--range M-N`, `--poll-interval N`.

- `--reset` — erase existing `ai/stress.json` and rebuild from scratch (without it, new results are merged with the existing file)

### Step 7 — Generate SSML

```bash
bookai ssml -p my-vampire-system
```

Converts each `translation/chapter_NNN.ru.txt` into `tts/chapter_NNN.ssml` (SSML with stress marks applied). Stress marks from the global `ai/stress.json` are applied to the text, then the text is wrapped in SSML tags (`<speak>`, `<p>`, `<s>`) for Silero TTS. This is local processing — no AI or network needed. Flags: `--force`, `--chapter N`, `--range M-N`.

### Step 8 — Synthesize audio

```bash
bookai tts -p my-vampire-system
```

Synthesizes each `tts/chapter_NNN.ssml` into `audio/chapter_NNN.mp3` (128kbps mono) via the Silero TTS server. The engine sends SSML text with `ssml: true` to the server's `POST /api/tts` endpoint. Long chapters are split into chunks under 900 characters; with `tts.parallel > 1` (default 1), chunks are synthesized concurrently and concatenated in order. Engines produce WAV internally; the CLI converts to MP3 via ffmpeg. To output WAV instead, set `tts.audio_format: wav` in `config.yaml`.

### Check progress at any time

```bash
bookai status -p my-vampire-system
```

Shows a directory tree and per-stage artifact counts.

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

# bookai

A local, file-based batch pipeline that turns an EPUB into a consistently-translated, glossary-backed audiobook.

> **Status: Milestone 3 complete.** EPUB import, chapter detection, glossary extraction, translation, text preprocessing with phonetic respellings, and XTTS v2 text-to-speech are implemented.

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
ai/          (respelling_NNN.json per chapter)
        │  7. bookai preprocess
        ▼
tts/         (chapter_NNN.txt)
        │  8. bookai tts                ← requires TTS server (Docker)
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

The XTTS v2 server runs in Docker and is optimized for NVIDIA Blackwell GPUs (RTX 50xx series). It stays running across books — start it once.

```bash
cd tts-server
docker compose up -d
```

The server listens on `http://localhost:8020`. On first synthesis, the XTTS v2 model (~1.8 GB) downloads from HuggingFace and caches in `tts-server/models/`. The container also bundles 119 voice samples — list them with:

```bash
curl -s http://localhost:8020/voices | python3 -m json.tool
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

> **Before continuing:** edit `my-vampire-system/config.yaml` to switch the TTS engine from `noop` to `xtts-http` and pick a voice (see [Configuration](#configuration) below).

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

### Step 6 — Generate respellings

```bash
bookai pronounce -p my-vampire-system
```

Scans each chapter's translation via the **OpenAI Batch API** for words that XTTS v2 will likely mispronounce. One batch item per chapter — the model sees the full chapter text and returns a list of {term, respelled} pairs. XTTS v2 does NOT support IPA phonemes or SSML — the only reliable way to control pronunciation is text replacement. The model generates respellings using rules like vowel doubling for stress (договор → договоор), ё→йо, and acronym expansion. Config `pronunciation` overrides are passed to the model. Writes per-chapter `ai/respelling_NNN.json`. Flags: `--force`, `--continue`, `--range M-N`, `--poll-interval N`.

### Step 7 — Preprocess translations

```bash
bookai preprocess -p my-vampire-system
```

Converts each `translation/chapter_NNN.ru.txt` into `tts/chapter_NNN.txt` (plain text with phonetic respellings applied). Per-chapter respellings from `ai/respelling_NNN.json` are replaced in the text, and Russian text normalization (de-capitalization of mid-sentence ALL-CAPS words) is applied. This replaces the old `ssml` command — since XTTS v2 doesn't support SSML, pronunciation control is done via text replacement before synthesis.

### Step 8 — Synthesize audio

```bash
bookai tts -p my-vampire-system
```

Synthesizes each `tts/chapter_NNN.txt` into `audio/chapter_NNN.mp3` (128kbps mono) via the XTTS v2 server. Engines produce WAV internally; the CLI converts to MP3 via ffmpeg. To output WAV instead, set `tts.audio_format: wav` in `config.yaml`.

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
bookai tts -p first-book --merge

bookai analyze-chapters -p second-book
bookai translate -p second-book
bookai tts -p second-book --merge
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
bookai preprocess --force               # regenerate all TTS text
bookai tts --force                      # re-synthesize all audio
```

To selectively clean a stage, remove its output directory and rerun:

```bash
rm -rf audio/ && bookai tts          # re-synthesize all audio
rm -rf tts/ && bookai preprocess     # regenerate all TTS text
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
  engine: xtts-http              # "noop" (default) or "xtts-http"
  language: ru
  server_url: http://localhost:8020
  speaker: eng/adult/male/MorganFreeman.wav  # any voice from /voices
  speed: 1.0
  audio_format: mp3              # "mp3" (default) or "wav"
  audio_bitrate: "128k"          # MP3 bitrate (default 128k)
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
pronunciation:                   # optional: lock respellings for XTTS v2
  - term: Куинн
    phonemes: КУинн               # phonetic respelling (plain Russian, not IPA)
  - term: Далки
    phonemes: ДАлки
```

The OpenAI API key is read from the `OPENAI_API_KEY` environment variable — it is **never** stored in `config.yaml`.

## Build

```bash
go build ./cmd/bookai
```

# bookai

A local, file-based batch pipeline that turns an EPUB into a consistently-translated, glossary-backed audiobook.

> **Status: Milestone 3 complete.** EPUB import, chapter detection, glossary extraction, translation, SSML generation, and XTTS v2 text-to-speech are implemented.

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
        │  6. bookai ssml
        ▼
tts/         (chapter_NNN.ssml)
        │  7. bookai tts                ← requires TTS server (Docker)
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

```bash
bookai import /path/to/book.epub "My Vampire System"   # creates ./my-vampire-system/
```

To import into an explicit directory (skips auto-creation):

```bash
bookai import /path/to/book.epub -p /existing/project-dir
```

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

### Step 3 — Extract glossary and characters

```bash
bookai analyze -p my-vampire-system
```

Uses `gpt-4.1-mini` to extract a glossary (terms + translations) and character list from chapter snippets. Writes `ai/glossary.json` and `ai/characters.json`.

Use `--chapter` or `--range` to analyze only a subset of chapters:

```bash
bookai analyze -p my-vampire-system --range 1-100
```

### Step 4 — Translate

```bash
bookai translate -p my-vampire-system
```

Translates each chapter to Russian using `gpt-4.1` with the glossary and previous chapter summaries as context. Writes `translation/chapter_NNN.ru.txt`, `memory/chapter_NNN.summary.txt`, and updates chapter status to `translated`.

This is the most time-consuming and expensive stage. Use `--chapter` or `--range` to translate in batches:

```bash
bookai translate -p my-vampire-system --range 1-50
bookai translate -p my-vampire-system --range 51-100
```

### Step 5 — Verify glossary (optional QA)

```bash
bookai verify-glossary -p my-vampire-system
```

Scans translations for untranslated English glossary terms. Writes `ai/glossary_violations.json`.

### Step 6 — Generate SSML

```bash
bookai ssml -p my-vampire-system
```

Converts each `translation/chapter_NNN.ru.txt` into `tts/chapter_NNN.ssml` (W3C SSML with `<p>`/`<s>`/`<phoneme>` tags). Pronunciation hints from `ai/pronunciation.json` are applied if present.

### Step 7 — Synthesize audio

```bash
bookai tts -p my-vampire-system
```

Synthesizes each `tts/chapter_NNN.ssml` into `audio/chapter_NNN.mp3` (128kbps mono) via the XTTS v2 server. Engines produce WAV internally; the CLI converts to MP3 via ffmpeg. To output WAV instead, set `tts.audio_format: wav` in `config.yaml`.

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
> bookai tts --merge
> ```

### Step 2 — Detect chapters

```bash
bookai analyze-chapters
```

Writes `chapters/chapter_NNN.json` (one per chapter), `chapters/_index.json`, and `chapters/_skipped.json` (non-chapter sections like TOC/notes).

### Step 3 — Extract glossary and characters

```bash
bookai analyze
```

Uses `gpt-4.1-mini` to extract a glossary (terms + translations) and character list from chapter snippets. Writes `ai/glossary.json` and `ai/characters.json`.

### Step 4 — Translate

```bash
bookai translate
```

Translates each chapter to Russian using `gpt-4.1` with the glossary and previous chapter summaries as context. Writes `translation/chapter_NNN.ru.txt`, `memory/chapter_NNN.summary.txt`, and updates chapter status to `translated`.

This is the most time-consuming and expensive stage. Use `--chapter` or `--range` to translate in batches:

```bash
bookai translate --range 1-50
bookai translate --range 51-100
```

### Step 5 — Verify glossary (optional QA)

```bash
bookai verify-glossary
```

Scans translations for untranslated English glossary terms. Writes `ai/glossary_violations.json`.

### Step 6 — Generate SSML

```bash
bookai ssml
```

Converts each `translation/chapter_NNN.ru.txt` into `tts/chapter_NNN.ssml` (W3C SSML with `<p>`/`<s>`/`<phoneme>` tags). Pronunciation hints from `ai/pronunciation.json` are applied if present.

### Step 7 — Synthesize audio

```bash
bookai tts
```

Synthesizes each `tts/chapter_NNN.ssml` into `audio/chapter_NNN.wav` via the XTTS v2 server. To merge all chapters into a single file:

```bash
bookai tts --merge      # produces audio/book.wav via ffmpeg
```

### Check progress at any time

```bash
bookai status
```

Shows a directory tree and per-stage artifact counts.

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
bookai ssml --force                     # regenerate all SSML
bookai tts --force                      # re-synthesize all audio
```

To selectively clean a stage, remove its output directory and rerun:

```bash
rm -rf audio/ && bookai tts        # re-synthesize all audio
rm -rf tts/ && bookai ssml         # regenerate all SSML
```

> **Note:** `ai/glossary.json` accumulates terms across chapters during translation. Deleting it and rerunning `bookai analyze` + `bookai translate --force` will rebuild it from scratch, but you'll lose any manual edits.

## Flags

All stage commands support:

| Flag | Description |
|------|-------------|
| `--force` | Regenerate artifacts even if they already exist |
| `--chapter N` | Process a single chapter (1-based) |
| `--range M-N` | Process a range of chapters |
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
  translation_model: gpt-4.1
  helper_model: gpt-4.1-mini
  max_retries: 3
tts:
  engine: xtts-http              # "noop" (default) or "xtts-http"
  language: ru
  server_url: http://localhost:8020
  speaker: eng/adult/male/MorganFreeman.wav  # any voice from /voices
  speed: 1.0
  audio_format: mp3              # "mp3" (default) or "wav"
  audio_bitrate: "128k"          # MP3 bitrate (default 128k)
```

The OpenAI API key is read from the `OPENAI_API_KEY` environment variable — it is **never** stored in `config.yaml`.

## Build

```bash
go build ./cmd/bookai
```

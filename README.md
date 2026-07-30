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
audio/       (chapter_NNN.wav, book.wav)
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

### Step 0 — Create a project directory

Each book gets its own directory. Create one and `cd` into it:

```bash
mkdir ~/books/my-vampire-system
cd ~/books/my-vampire-system
```

A `config.yaml` is auto-created with defaults on first run. To customize (e.g. pick a voice), create it upfront:

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
  engine: xtts-http
  language: ru
  server_url: http://localhost:8020
  speaker: eng/adult/male/MorganFreeman.wav  # any voice from /voices
  speed: 1.0
```

### Step 1 — Import the EPUB

```bash
bookai import /path/to/book.epub
```

Copies the EPUB to `source/original.epub` and extracts the spine, TOC, and per-item text blocks to `extracted/`.

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

Each book is an independent project directory. To process a different book:

```bash
mkdir ~/books/another-book
cd ~/books/another-book
bookai import /path/to/another.epub
# ... repeat steps 2–7
```

The TTS server (`tts-server/`) is shared across all books — don't start a second one. The `OPENAI_API_KEY` is also shared.

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

The `tts` command also supports `--merge` to concatenate all chapter WAVs into `audio/book.wav`.

## Build

```bash
go build ./cmd/bookai
```

# bookai

A local, file-based batch pipeline that turns an EPUB into a consistently-translated, glossary-backed audiobook.

> **Status: Milestone 3 complete.** EPUB import, chapter detection, glossary extraction, translation, SSML generation, and XTTS v2 text-to-speech are implemented.

## Pipeline

```
source/original.epub
        │  bookai import
        ▼
extracted/   (spine.json, toc.json, blocks/*.json)
        │  bookai analyze-chapters
        ▼
chapters/    (chapter_NNN.json, _skipped.json, _index.json)
        │  bookai analyze            [M2]
        ▼
ai/          (glossary.json, characters.json)
        │  bookai translate          [M2]
        ▼
translation/ + memory/   (chapter_NNN.ru.txt, chapter_NNN.summary.txt)
        │  bookai ssml              [M3]
        ▼
tts/         (chapter_NNN.ssml)
        │  bookai tts               [M3]
        ▼
audio/       (chapter_NNN.wav)
```

Every stage is idempotent: it skips artifacts that already exist unless `--force` is passed, and most stages accept `--chapter N` to target a single chapter.

## Quickstart (M1 + M2 + M3)

```bash
# from a fresh project directory
bookai import /path/to/book.epub
bookai analyze-chapters
bookai analyze          # extract glossary + characters (requires OPENAI_API_KEY)
bookai translate        # translate all chapters (requires OPENAI_API_KEY)
bookai verify-glossary  # check for untranslated terms
bookai ssml             # generate SSML from translations
bookai tts              # synthesize audio (requires TTS server, see below)
bookai tts --merge      # concatenate all chapter WAVs into audio/book.wav
bookai status
```

Translate or synthesize a single chapter or range:

```bash
bookai translate --chapter 5
bookai translate --range 1-10
bookai tts --chapter 5
bookai tts --range 1-10 --merge
```

## Configuration

`config.yaml` (auto-created with defaults if absent):

```yaml
project: my-book
languages:
  source: en
  target: ru
openai:
  translation_model: gpt-4.1
  helper_model: gpt-4.1-mini
  max_retries: 3
```

The OpenAI API key is read from the `OPENAI_API_KEY` environment variable — it is **never** stored in `config.yaml`.

### TTS configuration

To use the real XTTS v2 engine, set the following in `config.yaml`:

```yaml
tts:
  engine: xtts-http
  language: ru
  server_url: http://localhost:8020
  speaker: eng/adult/male/MorganFreeman.wav  # or any voice from /voices
  speed: 1.0
```

The default engine is `noop`, which generates sine-tone WAVs for pipeline testing without a TTS server.

### TTS server setup

The XTTS v2 server runs in Docker and is optimized for NVIDIA Blackwell GPUs (RTX 50xx series):

```bash
cd tts-server
# place a voice sample WAV in speakers/ (mono, 22050 Hz, 7-9 seconds)
docker compose up -d
```

The server will be available at `http://localhost:8020`. On first synthesis, the XTTS v2 model (~1.8 GB) downloads from HuggingFace and caches in `tts-server/models/`. See `tts-server/README.md` for details.

## Build

```bash
go build ./cmd/bookai
```

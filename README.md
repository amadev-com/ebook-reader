# bookai

A local, file-based batch pipeline that turns an EPUB into a consistently-translated, glossary-backed audiobook.

> **Status: Milestone 1 (in progress).** Only EPUB import and chapter detection are implemented. Translation, glossary, memory, and TTS stages are stubbed.

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

## Quickstart (M1)

```bash
# from a fresh project directory
bookai import /path/to/book.epub
bookai analyze-chapters
bookai status
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

## Build

```bash
go build ./cmd/bookai
```

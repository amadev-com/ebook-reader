# Silero TTS Server

REST server for Silero text-to-speech synthesis with SSML and stress mark support.

Based on [biblio-tts-server-silero](https://github.com/vpoluyaktov/biblio-tts-server-silero) — uses the prebuilt Docker image, no custom server code needed.

## Quick Start

```bash
docker compose up -d
```

The server will be available at `http://localhost:5555`.

- API docs: `http://localhost:5555/docs`
- OpenAPI spec: `http://localhost:5555/openapi.json`
- Health check: `http://localhost:5555/health`

## API Endpoints

- `POST /api/tts` — Synthesize speech (accepts JSON body with `text`, `voice`, `ssml`, `sample_rate`, `speed`, `pitch`)
- `GET /api/voices` — List available voices (filter by `language`)
- `GET /api/models` — List available models (filter by `language`)
- `GET /health` — Health check

## Configuration

The `docker-compose.yml` sets these environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `SILERO_DEVICE` | PyTorch device (`cpu` or `cuda`) | `cpu` |
| `SILERO_SERVED_MODELS` | Comma-separated models to serve | `v5_5_ru` |

## Available Russian Models

- `v5_ru`, `v5_2_ru`, `v5_3_ru`, `v5_4_ru`, `v5_5_ru`

## Available Russian Voices

| Voice ID | Speaker | Gender |
|----------|---------|--------|
| `silero:v5_5_ru#aidar` | Aidar | M |
| `silero:v5_5_ru#baya` | Baya | F |
| `silero:v5_5_ru#kseniya` | Kseniya | F |
| `silero:v5_5_ru#eugene` | Eugene | M |
| `silero:v5_5_ru#xenia` | Xenia | F |

## SSML Support

Silero supports the following SSML tags:

- `<speak>` — Root tag
- `<p>` — Paragraph (equivalent to `x-strong` pause)
- `<s>` — Sentence (equivalent to `strong` pause)
- `<break time="3s"/>` — Pause with specified duration
- `<prosody rate="x-slow">` — Speech rate (`x-slow`, `slow`, `medium`, `fast`, `x-fast`)
- `<prosody pitch="x-high">` — Pitch (`x-low`, `low`, `medium`, `high`, `x-high`)

## Stress Marks

Silero supports stress marks: a `+` before the stressed vowel.

Example: `к+едров` = stress on "е"

## bookai Configuration

In `config.yaml`:

```yaml
tts:
  engine: "silero-http"
  server_url: "http://localhost:5555"
  voice: "silero:v5_5_ru#xenia"
  sample_rate: 48000
  speed: 1.0
  pitch: 1.0
```

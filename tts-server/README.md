# Silero TTS Server

REST server for Silero text-to-speech synthesis with SSML and stress mark support.

Based on [biblio-tts-server-silero](https://github.com/vpoluyaktov/biblio-tts-server-silero) — uses the prebuilt Docker image with a custom entry point (`server.py`) that enables multi-worker parallel processing.

## Quick Start

```bash
docker compose up -d
```

The server will be available at `http://localhost:5555`.

- API docs: `http://localhost:5555/docs`
- OpenAPI spec: `http://localhost:5555/openapi.json`
- Health check: `http://localhost:5555/health`

## Model Cache

The `models/` directory contains pre-downloaded Silero model files (~140MB).
It is mounted read-only into the container at `/data/silero`, so all workers
load the model from local cache instead of downloading in parallel on first
start.

To populate the cache on a new machine:

```bash
# 1. Start the server without the models mount (comment out the volume in
#    docker-compose.yml), or use the original image directly:
docker run --rm -p 5555:5555 -e SILERO_SERVED_MODELS=v5_5_ru \
  vpoluyaktov/bibliohub-tts-server-silero:dev-latest

# 2. Trigger a synthesis request to download the model:
curl -X POST http://localhost:5555/api/tts \
  -H "Content-Type: application/json" \
  -d '{"text":"<speak><s>тест</s></speak>","voice":"silero:v5_5_ru#xenia","ssml":true,"sample_rate":48000}'

# 3. Copy the cache from the container:
docker cp <container_id>:/data/silero/. ./models/

# 4. Stop the container, then docker compose up -d with the mount active.
```

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
| `SILERO_WORKERS` | Number of parallel uvicorn worker processes | CPU cores (capped at 12) |
| `OMP_NUM_THREADS` | Torch/numpy threads per worker (keep at 1) | `1` |
| `MKL_NUM_THREADS` | MKL threads per worker (keep at 1) | `1` |

## Parallel Processing

The server runs multiple uvicorn worker processes, each with its own model copy
and a single torch thread. This enables true parallel request processing:

- **4 workers** = 4 concurrent TTS requests processed simultaneously
- Each worker uses 1 CPU core (no oversubscription)
- Model is small (~30MB), so 4 copies use ~120MB RAM total
- **~3x speedup** on real workloads (700-char chunks: 5.4s sequential → 1.7s parallel)

Tune `SILERO_WORKERS` to match your CPU core count. The `tts.parallel` setting in
`config.yaml` controls how many concurrent requests the bookai client sends per
chapter — set it to match `SILERO_WORKERS` for optimal throughput.

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
  parallel: 4                    # concurrent chunk requests (match SILERO_WORKERS)
```

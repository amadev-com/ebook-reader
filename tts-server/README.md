# XTTS v2 TTS Server

A Dockerized XTTS v2 text-to-speech server for the `bookai` pipeline.
Optimized for NVIDIA Blackwell GPUs (RTX 50xx series, SM 12.0).

## Architecture

Uses the `athomasson2/ebook2audiobook:cu130` Docker image as a base because
it provides CUDA 13.0 + PyTorch 2.11 + coqui-tts 0.27.5 — the only pre-built
image that supports Blackwell GPUs out of the box. The entrypoint is
overridden to run a minimal FastAPI server (`server.py`) instead of the full
ebook2audiobook Gradio app.

## Setup

1. Place voice sample WAV files in `speakers/` (mono, 22050 Hz, 7-9 seconds).
   The XTTS v2 server uses these for voice cloning.

2. Start the server:
   ```bash
   docker compose up -d
   ```

3. The server will be available at `http://localhost:8020`.
   API docs at `http://localhost:8020/docs`.

4. On first synthesis, the XTTS v2 model (~1.8 GB) will be downloaded from
   HuggingFace and cached in `models/`. Subsequent starts are fast.

## API

### `GET /health`
Returns GPU/CUDA availability and model load status.

### `GET /voices`
Lists available speaker WAV files in the `speakers/` directory.

### `POST /tts`
Synthesizes text to a WAV file. Returns `audio/wav`.

```json
{
  "text": "Привет мир",
  "language": "ru",
  "speaker_wav": "speaker_name.wav",
  "speed": 1.0
}
```

Alternatively, use `speaker_wav_path` to specify an absolute path to a WAV
file inside the container.

### `POST /tts_to_file`
Same as `/tts` but saves to a path inside the container and returns JSON.

## bookai configuration

In your project's `config.yaml`:

```yaml
tts:
  engine: xtts-http
  language: ru
  server_url: http://localhost:8020
  speaker: speaker_name.wav
  speed: 1.0
```

## Voice samples

Good voice samples are critical for quality. Guidelines:

- 7-9 seconds of clean, flowing speech
- Mono, 22050 Hz, 16-bit WAV
- No background noise or music
- No breathy sounds at start/end
- Show some vocal range

To prepare a sample with ffmpeg:
```bash
ffmpeg -i input.wav -ar 22050 -ac 1 -acodec pcm_s16le speakers/my_voice.wav
```

## Logs

```bash
docker compose logs -f
```

## Stop

```bash
docker compose down
```

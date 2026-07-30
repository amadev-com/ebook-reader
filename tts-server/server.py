#!/usr/bin/env python3
"""Minimal XTTS v2 FastAPI server.

Exposes a simple REST API for text-to-speech synthesis using Coqui XTTS v2.
Designed to run inside the athomasson2/ebook2audiobook:cu130 Docker image,
which provides CUDA 13.0 + PyTorch 2.11 + coqui-tts 0.27.5.

Endpoints:
  GET  /health         — health check, reports GPU availability
  GET  /voices         — list available speaker wav files
  POST /tts            — synthesize text to WAV (returns audio/wav)
  POST /tts_to_file    — synthesize text and save to a path inside the container

POST /tts body (JSON):
  {
    "text": "Привет мир",
    "language": "ru",
    "speaker_wav": "speaker_name",      # name in /app/speakers, or
    "speaker_wav_path": "/app/speakers/x.wav",  # absolute path
    "speed": 1.0                          # optional, default 1.0
  }
"""

import os
import sys
import logging
import glob
from pathlib import Path
from typing import Optional

import torch
from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse, Response
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("xtts-server")

SPEAKERS_DIR = os.environ.get("SPEAKERS_DIR", "/app/speakers")
OUTPUT_DIR = os.environ.get("OUTPUT_DIR", "/app/output")
MODEL_CACHE = os.environ.get("TTS_HOME", "/app/models")
COQUI_TOS_AGREED = os.environ.get("COQUI_TOS_AGREED", "1")
os.environ["COQUI_TOS_AGREED"] = COQUI_TOS_AGREED
os.environ["TTS_HOME"] = MODEL_CACHE

app = FastAPI(title="XTTS v2 Server", version="1.0.0")

_tts = None
_device = None


def get_device() -> str:
    if torch.cuda.is_available():
        return "cuda"
    return "cpu"


def get_tts():
    global _tts, _device
    if _tts is None:
        _device = get_device()
        logger.info("Loading XTTS v2 model on %s ...", _device)
        from TTS.api import TTS
        _tts = TTS("tts_models/multilingual/multi-dataset/xtts_v2", gpu=(_device == "cuda"))
        logger.info("XTTS v2 model loaded on %s", _device)
    return _tts


def resolve_speaker(speaker_wav: Optional[str], speaker_wav_path: Optional[str]) -> str:
    if speaker_wav_path:
        if not os.path.isfile(speaker_wav_path):
            raise HTTPException(status_code=400, detail=f"speaker_wav_path not found: {speaker_wav_path}")
        return speaker_wav_path
    if speaker_wav:
        # Look for a file or directory matching the name in speakers dir.
        candidate = os.path.join(SPEAKERS_DIR, speaker_wav)
        if os.path.isfile(candidate):
            return candidate
        if os.path.isdir(candidate):
            wavs = sorted(glob.glob(os.path.join(candidate, "*.wav")))
            if wavs:
                return wavs[0]
        # Try with .wav extension
        candidate_wav = candidate + ".wav"
        if os.path.isfile(candidate_wav):
            return candidate_wav
        raise HTTPException(status_code=400, detail=f"speaker '{speaker_wav}' not found in {SPEAKERS_DIR}")
    raise HTTPException(status_code=400, detail="either speaker_wav or speaker_wav_path is required")


class TTSRequest(BaseModel):
    text: str
    language: str = "ru"
    speaker_wav: Optional[str] = None
    speaker_wav_path: Optional[str] = None
    speed: float = 1.0


class TTSFileRequest(TTSRequest):
    output_path: str


@app.get("/health")
async def health():
    return {
        "status": "ok",
        "device": get_device(),
        "cuda_available": torch.cuda.is_available(),
        "cuda_version": torch.version.cuda if torch.cuda.is_available() else None,
        "torch_version": torch.__version__,
        "model_loaded": _tts is not None,
        "speakers_dir": SPEAKERS_DIR,
    }


@app.get("/voices")
async def list_voices():
    voices = []
    if os.path.isdir(SPEAKERS_DIR):
        for f in sorted(glob.glob(os.path.join(SPEAKERS_DIR, "*.wav"))):
            voices.append(os.path.basename(f))
        for d in sorted(glob.glob(os.path.join(SPEAKERS_DIR, "*/"))):
            wavs = sorted(glob.glob(os.path.join(d, "*.wav")))
            if wavs:
                voices.append(os.path.basename(d.rstrip("/")))
    return {"voices": voices}


@app.post("/tts")
async def synthesize(req: TTSRequest):
    if len(req.text) < 1:
        raise HTTPException(status_code=400, detail="text must not be empty")

    speaker = resolve_speaker(req.speaker_wav, req.speaker_wav_path)
    tts = get_tts()

    os.makedirs(OUTPUT_DIR, exist_ok=True)
    out_path = os.path.join(OUTPUT_DIR, "tts_output.wav")

    logger.info("Synthesizing %d chars in %s with speaker %s", len(req.text), req.language, speaker)
    try:
        tts.tts_to_file(
            text=req.text,
            language=req.language,
            speaker_wav=speaker,
            file_path=out_path,
            speed=req.speed,
        )
    except Exception as e:
        logger.error("Synthesis failed: %s", e)
        raise HTTPException(status_code=500, detail=str(e))

    return FileResponse(path=out_path, media_type="audio/wav", filename="output.wav")


@app.post("/tts_to_file")
async def synthesize_to_file(req: TTSFileRequest):
    if len(req.text) < 1:
        raise HTTPException(status_code=400, detail="text must not be empty")

    speaker = resolve_speaker(req.speaker_wav, req.speaker_wav_path)
    tts = get_tts()

    os.makedirs(os.path.dirname(req.output_path) or ".", exist_ok=True)

    logger.info("Synthesizing %d chars in %s to %s", len(req.text), req.language, req.output_path)
    try:
        tts.tts_to_file(
            text=req.text,
            language=req.language,
            speaker_wav=speaker,
            file_path=req.output_path,
            speed=req.speed,
        )
    except Exception as e:
        logger.error("Synthesis failed: %s", e)
        raise HTTPException(status_code=500, detail=str(e))

    return {"message": "ok", "output_path": req.output_path}


if __name__ == "__main__":
    import uvicorn
    port = int(os.environ.get("PORT", "8020"))
    host = os.environ.get("HOST", "0.0.0.0")
    logger.info("Starting XTTS v2 server on %s:%d", host, port)
    uvicorn.run(app, host=host, port=port, log_level="info")

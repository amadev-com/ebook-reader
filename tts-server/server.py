"""Parallel entry point for Biblio TTS Server (Silero).

Runs uvicorn with multiple worker processes so the server can handle
concurrent TTS requests in parallel. Each worker loads its own copy of
the Silero model (~30MB) and uses a single torch thread to avoid CPU
oversubscription.

Environment variables:
    SILERO_WORKERS  — number of uvicorn worker processes (default 4)
    SILERO_HOST      — bind host (default 0.0.0.0)
    SILERO_PORT      — bind port (default 80, mapped to 5555 in compose)
    SILERO_DEVICE    — "cpu" or "cuda" (default cpu)
    SILERO_SERVED_MODELS — comma-separated model IDs to preload
"""

import os

import torch

# Limit torch to 1 thread per worker process. With N workers, this gives
# N concurrent inferences using N cores total — no oversubscription.
torch.set_num_threads(1)
# Also limit OpenMP/MKL threads for numpy/scipy used in audio processing.
os.environ.setdefault("OMP_NUM_THREADS", "1")
os.environ.setdefault("MKL_NUM_THREADS", "1")

import uvicorn

from biblio_tts_server_silero.config import Settings

settings = Settings()

workers = int(os.environ.get("SILERO_WORKERS", "4"))
host = os.environ.get("SILERO_HOST", settings.host)
port = int(os.environ.get("SILERO_PORT", str(settings.port)))

if __name__ == "__main__":
    uvicorn.run(
        "biblio_tts_server_silero.app:app",
        host=host,
        port=port,
        workers=workers,
        reload=False,
    )

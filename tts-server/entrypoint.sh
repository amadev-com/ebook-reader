#!/bin/sh
# Entry point for the Silero TTS server with stress endpoint.
# Installs silero-stress if not already installed, then runs server.py.

set -e

if ! python -c "import silero_stress" 2>/dev/null; then
    echo "[entrypoint] installing silero-stress..."
    pip install --no-cache-dir silero-stress
fi

exec python /app/server.py

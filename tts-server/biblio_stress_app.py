"""Wrapper app that mounts the stress endpoint onto the Biblio TTS Server.

This module imports the original FastAPI app from biblio_tts_server_silero
and adds the /api/stress endpoint from stress_endpoint.py. It's used as
the uvicorn app target in server.py so each worker gets both the TTS
endpoints and the stress endpoint.

Usage in server.py:
    uvicorn.run("biblio_stress_app:app", ...)
"""

from biblio_tts_server_silero.app import app

from stress_endpoint import mount_stress_endpoint

mount_stress_endpoint(app)

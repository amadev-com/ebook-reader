"""Stress placement endpoint for Silero TTS server.

Adds a `/api/stress` endpoint to the existing Biblio TTS Server (Silero)
that applies automatic stress placement using the silero-stress package.
The endpoint accepts a batch of sentences and returns each sentence with
`+` marks before stressed vowels (Silero TTS convention).

The accentor is loaded once per worker process (same as the TTS model).
It uses the same torch thread limit (1) set in server.py.

Endpoint:
    POST /api/stress
    Body: {"sentences": ["Меня зовут Лева.", "Я из готов."]}
    Response: {"results": ["Мен+я зов+ут Л+ёва.", "+Я +из г+отов."]}
"""

import logging
import os

from fastapi import APIRouter, FastAPI
from pydantic import BaseModel

logger = logging.getLogger(__name__)

# Lazy-load the accentor on first request. This avoids loading the model
# if the stress endpoint is never used (e.g. when only TTS is needed).
_accentor = None


def _get_accentor():
    global _accentor
    if _accentor is None:
        logger.info("loading silero-stress accentor")
        from silero_stress import load_accentor
        _accentor = load_accentor()
        logger.info("silero-stress accentor loaded")
    return _accentor


class StressRequest(BaseModel):
    sentences: list[str]


class StressResponse(BaseModel):
    results: list[str]


router = APIRouter()


@router.post("/api/stress", response_model=StressResponse)
def stress_text(req: StressRequest) -> StressResponse:
    """Apply stress marks to a batch of sentences.

    Each sentence is processed independently by the silero-stress accentor,
    which handles homograph disambiguation within the sentence context.
    """
    accentor = _get_accentor()
    results = []
    for sentence in req.sentences:
        if not sentence or not sentence.strip():
            results.append(sentence)
            continue
        try:
            stressed = accentor(sentence)
            results.append(stressed)
        except Exception as e:
            logger.warning("stress failed for sentence, returning original: %s", e)
            results.append(sentence)
    return StressResponse(results=results)


def mount_stress_endpoint(app: FastAPI) -> None:
    """Mount the stress router onto an existing FastAPI app."""
    app.include_router(router)

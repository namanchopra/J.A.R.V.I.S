"""Append-only JSONL event store for the Jarvis voice daemon.

Persists JarvisEvent instances to ``~/.awm/jarvis-events.jsonl`` so events survive
daemon restarts and can be queried for briefings ("what happened while I was
away?").

Format: one JSON object per line (JSONL).  Corrupt lines are silently skipped
during reads.  Auto-rotation on startup prunes events older than 24 hours and
caps the file at 10 000 entries.
"""

from __future__ import annotations

import json
import logging
import time
from dataclasses import asdict
from pathlib import Path
from typing import Final

logger: Final = logging.getLogger("jarvis-daemon.events")

EVENTS_PATH: Final = Path.home() / ".awm" / "jarvis-events.jsonl"
MAX_AGE_HOURS: Final[int] = 24
MAX_EVENTS: Final[int] = 10000


class EventStore:
    """Append-only JSONL event store at ~/.awm/jarvis-events.jsonl.

    Events are JarvisEvent dataclass instances (from priority.py).
    Auto-rotates: prunes events older than 24h on startup.
    """

    def __init__(self, path: Path = EVENTS_PATH) -> None:
        self._path = path
        self._path.parent.mkdir(parents=True, exist_ok=True)

    def rotate(self) -> None:
        """Remove events older than MAX_AGE_HOURS.  Call on startup."""
        if not self._path.exists():
            return
        cutoff = time.time() - (MAX_AGE_HOURS * 3600)
        kept: list[str] = []
        try:
            with open(self._path) as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        data = json.loads(line)
                        if data.get("timestamp", 0) >= cutoff:
                            kept.append(line)
                    except json.JSONDecodeError:
                        continue
            # Rewrite with only recent events
            with open(self._path, "w") as f:
                for line in kept[-MAX_EVENTS:]:
                    f.write(line + "\n")
            logger.info(
                "Event store rotated: kept %d events (pruned older than %dh)",
                len(kept),
                MAX_AGE_HOURS,
            )
        except Exception:
            logger.exception("Event store rotation failed")

    def append(self, event: object) -> None:
        """Append a JarvisEvent to the store."""
        try:
            data = asdict(event) if hasattr(event, "__dataclass_fields__") else event
            with open(self._path, "a") as f:
                f.write(json.dumps(data) + "\n")
        except Exception:
            logger.exception("Failed to append event")

    def get_recent(self, since_minutes: int = 15) -> list[dict]:
        """Return events from the last *since_minutes* minutes."""
        if not self._path.exists():
            return []
        cutoff = time.time() - (since_minutes * 60)
        results: list[dict] = []
        try:
            with open(self._path) as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        data = json.loads(line)
                        if data.get("timestamp", 0) >= cutoff:
                            results.append(data)
                    except json.JSONDecodeError:
                        continue
        except Exception:
            logger.exception("Failed to read events")
        return results

    def get_all(self) -> list[dict]:
        """Return all events (last 24h after rotation)."""
        return self.get_recent(since_minutes=MAX_AGE_HOURS * 60)

    def clear(self) -> None:
        """Delete all events."""
        if self._path.exists():
            self._path.unlink()

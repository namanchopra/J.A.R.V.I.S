import json
import logging
import os
from dataclasses import dataclass, field, asdict
from pathlib import Path

logger = logging.getLogger("jarvis-daemon.memory")

HISTORY_PATH = Path.home() / ".awm" / "jarvis-history.json"
MAX_TURNS = 50  # Keep last 50 user+assistant pairs

@dataclass
class Message:
    role: str       # "user" or "assistant"
    content: str
    timestamp: float = 0.0

class ConversationMemory:
    """Persistent conversation history for Jarvis.

    Stores messages to ~/.awm/jarvis-history.json.
    Loads on startup, saves after each message.
    Prunes to MAX_TURNS to keep file/context manageable.
    """

    def __init__(self, path: Path = HISTORY_PATH, max_turns: int = MAX_TURNS):
        self._path = path
        self._max_turns = max_turns
        self._messages: list[Message] = []

    def load(self):
        """Load history from disk. Handles missing/corrupt files gracefully."""
        if not self._path.exists():
            logger.info("No history file, starting fresh")
            return
        try:
            data = json.loads(self._path.read_text())
            if isinstance(data, list):
                self._messages = [
                    Message(role=m.get("role", "user"), content=m.get("content", ""), timestamp=m.get("timestamp", 0))
                    for m in data if isinstance(m, dict) and m.get("content")
                ]
                self._prune()
                logger.info(f"Loaded {len(self._messages)} messages from history")
            else:
                logger.warning("History file has unexpected format, starting fresh")
        except (json.JSONDecodeError, KeyError, TypeError) as e:
            logger.warning(f"Corrupt history file, starting fresh: {e}")
            self._messages = []

    def save(self):
        """Save history to disk."""
        self._path.parent.mkdir(parents=True, exist_ok=True)
        data = [asdict(m) for m in self._messages]
        self._path.write_text(json.dumps(data, indent=2))

    def add(self, role: str, content: str):
        """Add a message and auto-save."""
        import time
        self._messages.append(Message(role=role, content=content, timestamp=time.time()))
        self._prune()
        self.save()

    def get_messages(self) -> list[dict]:
        """Return messages as dicts for LLM consumption."""
        return [{"role": m.role, "content": m.content} for m in self._messages]

    def get_context_summary(self) -> str:
        """Generate a brief summary of recent conversation for LLM context.

        Returns a <200 token string like:
        "Earlier, sir asked about maya-web status. You approved auth-service.
         Sir mentioned focusing on the hooli platform."
        """
        if not self._messages:
            return ""

        # Take last 10 messages and summarize
        recent = self._messages[-10:]
        parts = []
        for m in recent:
            if m.role == "user":
                content = m.content[:80]
                parts.append(f"Sir said: \"{content}\"")
            else:
                content = m.content[:80]
                parts.append(f"You replied: \"{content}\"")

        return "Recent conversation:\n" + "\n".join(parts[-6:])  # Last 6 entries

    def clear(self):
        """Clear all history."""
        self._messages = []
        if self._path.exists():
            self._path.unlink()

    def _prune(self):
        """Keep only the last max_turns messages."""
        max_msgs = self._max_turns * 2  # 2 messages per turn
        if len(self._messages) > max_msgs:
            self._messages = self._messages[-max_msgs:]

    def __len__(self) -> int:
        return len(self._messages)

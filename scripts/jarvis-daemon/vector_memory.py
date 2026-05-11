"""ChromaDB-backed semantic memory for Jarvis.

Stores conversation turns as vector embeddings for semantic search,
enabling Jarvis to recall past conversations by meaning rather than
just recency. Persistent across daemon restarts (stored at
``~/.awm/chromadb/``).

Uses ChromaDB's built-in default embedding model -- no API key needed.
If ``chromadb`` is not installed the module degrades gracefully:
:pyattr:`VectorMemory.available` stays ``False`` and all public methods
become safe no-ops that return empty results.

Usage::

    from vector_memory import VectorMemory

    vm = VectorMemory()
    vm.start()                            # once, on daemon startup
    vm.store("We fixed the auth bug in maya-service", role="user")
    results = vm.search("auth bug")       # -> [{text, role, timestamp, distance}]
    context = vm.get_context("auth bug")  # -> formatted string for LLM prompt
"""

from __future__ import annotations

import logging
import time
import uuid
from pathlib import Path
from typing import Any, Final

logger: Final = logging.getLogger("jarvis-daemon.vector_memory")

DB_PATH: Final = str(Path.home() / ".awm" / "chromadb")
COLLECTION_NAME: Final = "jarvis_memory"
MAX_RESULTS: Final = 5


class VectorMemory:
    """Persistent semantic memory using ChromaDB.

    Stores conversation turns as embeddings for semantic search.
    Persistent across daemon restarts (stored at ~/.awm/chromadb/).
    Uses ChromaDB's built-in default embedding model (no API key needed).
    """

    def __init__(self, path: str = DB_PATH) -> None:
        self._path = path
        self._collection: Any = None
        self._available: bool = False

    def start(self) -> None:
        """Initialize ChromaDB. Call once on daemon startup."""
        try:
            import chromadb

            client = chromadb.PersistentClient(path=self._path)
            self._collection = client.get_or_create_collection(
                name=COLLECTION_NAME,
                metadata={"hnsw:space": "cosine"},
            )
            self._available = True
            count = self._collection.count()
            logger.info("Vector memory started: %d memories at %s", count, self._path)
        except ImportError:
            logger.warning("chromadb not installed -- vector memory disabled")
        except Exception:
            logger.exception("Vector memory failed to start")

    @property
    def available(self) -> bool:
        """Whether ChromaDB initialised successfully."""
        return self._available

    def store(
        self,
        text: str,
        role: str = "user",
        metadata: dict[str, Any] | None = None,
    ) -> None:
        """Store a conversation turn as a vector embedding."""
        if not self._available or not text.strip():
            return
        try:
            doc_id = str(uuid.uuid4())
            meta: dict[str, Any] = {
                "role": role,
                "timestamp": time.time(),
                **(metadata or {}),
            }
            self._collection.add(
                ids=[doc_id],
                documents=[text],
                metadatas=[meta],
            )
        except Exception:
            logger.debug("Failed to store memory", exc_info=True)

    def search(self, query: str, n_results: int = MAX_RESULTS) -> list[dict[str, Any]]:
        """Semantic search across all stored memories.

        Returns list of ``{text, role, timestamp, distance}`` dicts,
        ordered by relevance (closest first).
        """
        if not self._available or not query.strip():
            return []
        try:
            results = self._collection.query(
                query_texts=[query],
                n_results=min(n_results, self.count()) if self.count() > 0 else n_results,
            )
            memories: list[dict[str, Any]] = []
            if results and results["documents"] and results["documents"][0]:
                docs = results["documents"][0]
                metas = (
                    results["metadatas"][0]
                    if results["metadatas"]
                    else [{}] * len(docs)
                )
                dists = (
                    results["distances"][0]
                    if results["distances"]
                    else [0.0] * len(docs)
                )

                for doc, meta, dist in zip(docs, metas, dists):
                    memories.append({
                        "text": doc,
                        "role": meta.get("role", "unknown"),
                        "timestamp": meta.get("timestamp", 0),
                        "distance": dist,
                    })
            return memories
        except Exception:
            logger.debug("Memory search failed", exc_info=True)
            return []

    def get_context(self, query: str, max_tokens: int = 500) -> str:
        """Build a context string from relevant memories for the LLM prompt.

        Returns a formatted string of relevant past conversations,
        or empty string if nothing relevant found.
        """
        memories = self.search(query, n_results=3)
        if not memories:
            return ""

        parts: list[str] = []
        total_chars = 0
        for m in memories:
            # Skip very distant matches (not really relevant).
            if m["distance"] > 1.5:
                continue

            text = m["text"][:200]  # Truncate long memories
            role = "Sir" if m["role"] == "user" else "Jarvis"

            line = f"- {role}: {text}"
            # Rough char-to-token estimate (1 token ~= 4 chars).
            if total_chars + len(line) > max_tokens * 4:
                break
            parts.append(line)
            total_chars += len(line)

        if not parts:
            return ""

        return "Relevant past conversations:\n" + "\n".join(parts)

    def count(self) -> int:
        """Total number of stored memories."""
        if not self._available:
            return 0
        try:
            return self._collection.count()
        except Exception:
            return 0

    def clear(self) -> None:
        """Delete all memories and recreate the collection."""
        if not self._available:
            return
        try:
            import chromadb

            client = chromadb.PersistentClient(path=self._path)
            client.delete_collection(COLLECTION_NAME)
            self._collection = client.get_or_create_collection(
                name=COLLECTION_NAME,
                metadata={"hnsw:space": "cosine"},
            )
            logger.info("Vector memory cleared")
        except Exception:
            logger.exception("Failed to clear vector memory")

"""Meeting summary + markdown writer (TASK-007 of jarvis-meeting-mode).

Consumes the transcript buffer accumulated by TASK-006 during a meeting,
runs a one-shot LLM call to produce a structured summary, writes the
result to a Markdown file under MeetingNotesDir, and returns a tuple of
(markdown_path, recap_text). The recap is a 2-sentence prose summary
intended for TASK-008's spoken-recap TTS pass.

This module is purposely standalone: no daemon imports, no global state.
The caller passes in the LLM service handle so the file is testable
without spinning up the full pipeline.

Failure modes (each tested by TASK-013):
  - Empty buffer (someone hit start then immediately stop): write a stub
    markdown file with "(no audio captured)" and return an empty recap.
    Do NOT invoke the LLM in this case -- wasted call.
  - LLM call fails (network, quota): fall back to "raw transcript only"
    markdown (header + Raw Transcript section, no Summary/Key Points/
    Action Items). The user keeps the raw data; better than crashing.
  - Filename collision: append "-2", "-3" until a free path is found.

LLM call shape:
  The daemon's ``AnthropicLLMService`` (from pipecat) exposes its
  underlying ``AsyncAnthropic`` client at ``service._client`` and the
  configured model name at ``service._settings.model``. We use those
  directly for a one-shot ``messages.create(...)`` call rather than
  routing through the full Pipecat frame pipeline (which is wired for
  streaming TTS, not one-shot summarisation). If those attributes are
  unavailable (different service version, or ``None`` passed by caller)
  the call returns ``None`` and the caller falls back to raw-only.
"""

from __future__ import annotations

import logging
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Final

logger = logging.getLogger("jarvis-daemon.meeting_notes")

# LLM system prompt for the summarisation pass. The format constraints
# (exact section headings, action-item shape) are critical -- any
# downstream consumer (the user, future Slack/Linear push integrations)
# parses on these headings.
MEETING_SYSTEM_PROMPT: Final[str] = """You are a meeting note-taker. You are given a
transcript of a meeting between the user ("user", from their microphone)
and other participants ("other", from system audio capture of the call).
Each line is timestamped and tagged with its source.

Output a Markdown document with EXACTLY these four sections in this order:

## Summary
2-4 sentence prose summary of what was discussed and decided.

## Key Points
- Bulleted list of the most important points raised. Brief.

## Action Items
- [ ] <action> -- owner: <name>, due: <when>
- [ ] <action> -- owner: <name>, due: <when>

If no clear owner or due date is mentioned, write "owner: unassigned"
or "due: tbd" -- don't invent dates or names.

## Raw Transcript
The literal transcript content, ALREADY PROVIDED by the caller. Do not
re-generate this section -- leave a placeholder ":raw-transcript:" and
the caller will substitute the real transcript text in.

End your output after section "## Raw Transcript :raw-transcript:" -- no
trailing prose, no closing remarks."""

# Cap on the recap text (chars). The brief allows up to 250.
RECAP_MAX_CHARS: Final[int] = 250

# Max tokens for the one-shot summary call. Generous enough for a
# multi-page meeting summary while keeping the call cheap.
_LLM_MAX_TOKENS: Final[int] = 1024


def slugify(s: str) -> str:
    """Lowercase, ASCII, dashes-for-spaces; strip everything non-[a-z0-9-].

    Empty input becomes "untitled" so the filename always has a slug
    segment (downstream collision-suffix logic relies on a non-empty base).
    """
    s = (s or "").strip().lower()
    s = re.sub(r"[^a-z0-9]+", "-", s)
    s = s.strip("-")
    return s or "untitled"


def _format_raw_transcript(buffer: list[dict[str, Any]]) -> str:
    """Render the buffer entries as a Markdown-friendly transcript block.

    Each entry produces a single line:

        - **[14:30:12]** _mic_: User said something here.

    Times are localised to the user's wall clock (parsed from the
    entry's ts ISO 8601 field).
    """
    if not buffer:
        return "_(no transcript entries)_"
    lines: list[str] = []
    for entry in buffer:
        ts = entry.get("ts", "")
        try:
            dt = datetime.fromisoformat(ts)
            hhmmss = dt.astimezone().strftime("%H:%M:%S")
        except (ValueError, TypeError):
            hhmmss = "??:??:??"
        source = entry.get("source", "mic")
        text = (entry.get("text") or "").strip()
        if not text:
            continue
        lines.append(f"- **[{hhmmss}]** _{source}_: {text}")
    return "\n".join(lines) if lines else "_(no transcript entries)_"


def _resolve_notes_dir(notes_dir: str) -> Path:
    """Expand ~ in the configured notes dir and ensure it exists.

    Defensive: an empty notes_dir falls back to ~/.jarvis/meetings (which
    config.Load() guarantees, but we mirror the fallback so tests don't
    need to mock the config layer).
    """
    if not notes_dir:
        notes_dir = "~/.jarvis/meetings"
    p = Path(notes_dir).expanduser()
    p.mkdir(parents=True, exist_ok=True)
    return p


def _resolve_filename(notes_dir: Path, slug: str, now: datetime) -> Path:
    """Return a fresh filename under notes_dir of the form
    YYYY-MM-DD-HH-MM-<slug>.md, with -2/-3 suffixing on collision."""
    base = now.strftime("%Y-%m-%d-%H-%M")
    candidate = notes_dir / f"{base}-{slug}.md"
    if not candidate.exists():
        return candidate
    suffix = 2
    while True:
        candidate = notes_dir / f"{base}-{slug}-{suffix}.md"
        if not candidate.exists():
            return candidate
        suffix += 1
        if suffix > 100:
            # Almost certainly a bug if we hit this -- log and give up.
            logger.error("meeting_notes: too many collisions on %s", base)
            return candidate  # overwrite as a last resort


def _format_header(title: str, started_at_utc: datetime, n_entries: int) -> str:
    """Compact header block at the top of every meeting markdown file."""
    local = started_at_utc.astimezone()
    return (
        f"# {title}\n\n"
        f"> Captured: {local.strftime('%A, %B %d %Y at %H:%M')} "
        f"({local.tzname()})\n"
        f"> Transcript entries: {n_entries}\n"
    )


async def _call_llm_for_summary(
    llm_service: Any,
    title: str,
    raw_transcript: str,
) -> str | None:
    """Run a one-shot LLM call to produce the Summary / Key Points /
    Action Items sections. Returns the LLM's raw markdown output, or
    None on failure (network, quota, missing service attrs) -- caller
    falls back to a raw-transcript-only file.

    Implementation: we reach into ``llm_service._client`` (the underlying
    ``AsyncAnthropic`` async client that pipecat's ``AnthropicLLMService``
    wraps) and call ``messages.create(...)`` directly. The streaming
    Pipecat path is wired for live TTS and isn't appropriate for a
    one-shot offline summary. The model name comes from
    ``llm_service._settings.model``.

    If ``llm_service`` is None or doesn't expose the expected attrs we
    log a warning and return None -- the caller writes a raw-only file.
    """
    if llm_service is None:
        logger.warning(
            "meeting_notes: no llm_service handle; falling back to raw-only"
        )
        return None

    client = getattr(llm_service, "_client", None)
    settings = getattr(llm_service, "_settings", None)
    model = getattr(settings, "model", None) if settings is not None else None
    if client is None or model is None:
        logger.warning(
            "meeting_notes: llm_service missing _client/_settings.model; "
            "falling back to raw-only (client=%s, model=%s)",
            client is not None,
            model,
        )
        return None

    user_msg = (
        f"Meeting title: {title or 'Untitled'}\n\n"
        f"Transcript:\n{raw_transcript}\n\n"
        "Produce the four-section markdown per the system prompt."
    )

    try:
        response = await client.messages.create(
            model=model,
            max_tokens=_LLM_MAX_TOKENS,
            system=MEETING_SYSTEM_PROMPT,
            messages=[{"role": "user", "content": user_msg}],
        )
    except Exception as exc:  # noqa: BLE001 - all-or-nothing on LLM call
        logger.warning("meeting_notes: LLM summary call failed: %r", exc)
        return None

    # Anthropic's response.content is a list of content blocks; pull the
    # text from the first text block. Defensive against the SDK shifting
    # shape between versions.
    try:
        blocks = getattr(response, "content", None) or []
        for block in blocks:
            text = getattr(block, "text", None)
            if text:
                return text
        # Last resort: stringify the response so we have *something*.
        logger.warning(
            "meeting_notes: LLM response had no text block; got %r", response
        )
        return None
    except Exception as exc:  # noqa: BLE001
        logger.warning(
            "meeting_notes: failed to extract text from LLM response: %r", exc
        )
        return None


def _extract_recap(summary_markdown: str) -> str:
    """Pull a 2-sentence recap from the LLM output for TASK-008's TTS.

    Strategy: take the Summary section's text content, strip the markdown
    heading, return the first ~250 chars. If parsing fails, return an
    empty string -- TASK-008 will skip the spoken-recap step.
    """
    if not summary_markdown:
        return ""
    # Find "## Summary" and grab until the next "## " heading.
    match = re.search(
        r"##\s+Summary\s*\n+(.+?)(?=\n##\s|\Z)",
        summary_markdown,
        re.DOTALL | re.IGNORECASE,
    )
    if not match:
        return ""
    text = match.group(1).strip()
    # Collapse whitespace + clip.
    text = re.sub(r"\s+", " ", text)
    if len(text) > RECAP_MAX_CHARS:
        text = text[: RECAP_MAX_CHARS - 1].rsplit(" ", 1)[0] + "..."
    return text


async def generate_meeting_notes(
    title: str,
    buffer: list[dict[str, Any]],
    llm_service: Any,
    notes_dir: str = "~/.jarvis/meetings",
) -> tuple[str, str]:
    """Run the summarisation pipeline + write the markdown file.

    Returns ``(markdown_path, recap_text)``. recap_text is empty when
    the buffer is empty OR when the LLM call failed.

    Failure cases handled inline (each is a separate acceptance criterion
    in TASK-007's brief):
      - Empty buffer: write a stub file ("(no audio captured)"), return
        empty recap, do NOT invoke the LLM.
      - LLM failure: write a fallback file with header + raw transcript
        only, return empty recap.
      - Filename collision: append -2, -3, ... until a free path is found.
    """
    title = (title or "").strip()
    if not title:
        title = "Untitled meeting"

    now = datetime.now(timezone.utc)
    slug = slugify(title)
    dir_path = _resolve_notes_dir(notes_dir)
    target = _resolve_filename(dir_path, slug, now.astimezone())
    header = _format_header(title, now, len(buffer))
    raw_transcript = _format_raw_transcript(buffer)

    # ---- Empty-buffer fast path: no LLM call ----
    if not buffer:
        body = (
            header
            + "\n\n## Summary\n_(no audio captured)_\n"
            + "\n## Key Points\n_(none)_\n"
            + "\n## Action Items\n_(none)_\n"
            + "\n## Raw Transcript\n_(no transcript entries)_\n"
        )
        target.write_text(body, encoding="utf-8")
        logger.info("meeting_notes: empty buffer, wrote stub to %s", target)
        return (str(target), "")

    # ---- Normal path: ask the LLM ----
    llm_md = await _call_llm_for_summary(llm_service, title, raw_transcript)
    if llm_md is None:
        # ---- LLM-failure fallback path ----
        body = (
            header
            + "\n\n## Raw Transcript\n"
            + raw_transcript
            + "\n\n_(Summary generation failed; raw transcript saved above.)_\n"
        )
        target.write_text(body, encoding="utf-8")
        logger.warning(
            "meeting_notes: LLM failed; wrote raw-only file to %s", target
        )
        return (str(target), "")

    # Substitute the literal transcript into the LLM's placeholder.
    full_md = header + "\n\n" + llm_md.replace(":raw-transcript:", raw_transcript)
    target.write_text(full_md, encoding="utf-8")
    recap = _extract_recap(llm_md)
    logger.info(
        "meeting_notes: wrote %s (%d chars), recap=%d chars",
        target,
        len(full_md),
        len(recap),
    )
    return (str(target), recap)

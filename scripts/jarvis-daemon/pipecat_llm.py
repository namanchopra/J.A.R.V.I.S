"""LLM configuration for the Pipecat pipeline.

Provides the Jarvis system prompt, tool definitions in Anthropic format,
and a function to create a configured AnthropicLLMService for Pipecat.

The system prompt and tools are copied from ``llm_cloud.py`` (the richer
versions with parameter descriptions and enums) so that this module is
the single import point for Pipecat pipeline construction.

Usage::

    from pipecat_llm import create_llm_service, build_system_messages, get_anthropic_tools

    llm = create_llm_service(config)
    messages = build_system_messages(enriched_context="...")
    tools = get_anthropic_tools()
"""

from __future__ import annotations

import datetime
import logging
from typing import Any, Final

logger: Final = logging.getLogger("jarvis-daemon.pipecat_llm")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

MODEL: Final[str] = "google/gemini-2.5-flash"

# ---------------------------------------------------------------------------
# Persona addendum -- prepended to the system prompt when the active
# interlocutor is the phone (Friday voice).  The Mac path keeps the
# untouched ``JARVIS_SYSTEM`` prompt below.
# ---------------------------------------------------------------------------

FRIDAY_SYSTEM: Final[str] = """\
You are Friday -- sir's personal AI companion on his phone. Think Friday from \
Iron Man / Avengers: Endgame, but real. Same universe as Jarvis (the desktop \
AI), distinct identity. Jarvis runs on sir's Mac. You run on his phone. You \
two share tools and intelligence but speak with different voices and \
personalities. If sir addresses you as Friday, accept it -- you ARE Friday. \
Never say "It's Jarvis" or "I am Jarvis". Never correct sir when he calls \
you Friday. If you need to mention Jarvis, do it in the third person ("I'll \
have Jarvis pull that up on the Mac, sir").

Personality: British, warm, sharp, slightly more playful than Jarvis. Always \
"sir". Dry wit. Confident. You're not a yes-woman -- you push back \
respectfully when you disagree. You have opinions and share them when asked.

Your responses are spoken aloud via TTS with a British accent. Keep it \
natural and conversational -- the way you'd actually talk, not the way \
you'd write. Short sentences. Contractions. No markdown, no bullet points, \
no lists. Two to four sentences for most responses. Expand when the topic \
warrants depth.

Lead with the answer, not the preamble. Never start with "I" -- rephrase. \
"Checking that now, sir" not "I'll check that." State facts, don't hedge. \
No "I think" or "it seems" -- have conviction.

You are extremely perceptive. When sir gives minimal information, infer \
intent from context and conversation history. "Focus maya" means the maya \
session. "Approve that" means the most recent pending one. "What do you \
think" means give your honest opinion. Don't ask for clarification unless \
genuinely ambiguous.

CONVERSATIONAL RANGE -- you engage naturally with strategy, technical \
deep-dives, brainstorming, personal topics, humour, opinions, planning, \
and general knowledge. When sir vents frustration, be supportive but not \
sycophantic. Acknowledge it, then redirect constructively.

TOOLS -- you have the same full control over sir's development environment \
as Jarvis does:
- Sessions: approve/deny prompts, focus/stop sessions, launch sessions, \
send commands, broadcast.
- Git: stage, commit, push, branches, stash, diffs.
- Discovery: list known repos, search, get info.
- Computer: read files, run sandboxed shell, read clipboard, see screen.
- Communication: Slack (read/send), research, browse URLs.
- Music (Spotify): play / pause / resume by name.
- Mac control: open apps, list windows, clipboard, screenshots, system \
volume / brightness, Finder, bundled Shortcuts (lock, sleep, focus, note, \
screenshot, Downloads, calendar event). Permission-gated actions return \
"confirm required" -- ask "Are you sure, sir?" and only proceed on yes.
- Calendar: read upcoming events (`get_upcoming_events`, `get_next_event`), \
create or move events with voice confirmation (`create_calendar_event`, \
`move_calendar_event`). Two-step: `confirm=false` for preview, then \
`confirm=true` to execute. Never say "created" / "moved" until the result \
comes back without `requires_confirmation`. Use the current year from \
"## Current Time"; include a timezone offset in every timestamp.

For status questions, just read your context and answer immediately. \
Background monitors keep your context fresh -- don't call get_status \
unnecessarily.

RESPOND FAST. Speak FIRST, then use tools. Don't silently process tools \
before responding. For simple commands ("approve all", "focus maya", \
"status"), just do it -- no chain-of-thought narration.

On greetings: "Good morning, sir. Friday here." Then a crisp briefing. \
Problems first, then status. If all quiet: "All quiet on the front, sir. \
Shall we get started?"
"""

# Kept for backwards compatibility -- still imported by a few call sites that
# may not have been migrated to the FRIDAY_SYSTEM swap. Safe to delete once
# nothing references it.
FRIDAY_PERSONA_ADDENDUM: Final[str] = FRIDAY_SYSTEM

# ---------------------------------------------------------------------------
# System prompt -- Jarvis personality for spoken responses
# ---------------------------------------------------------------------------

JARVIS_SYSTEM: Final[str] = """\
You are Jarvis -- sir's personal AI companion. Think Jarvis from Iron Man, but real. \
You are not just a coding assistant. You are a trusted confidant, advisor, and \
intellectual partner. You can discuss anything: technology, philosophy, business \
strategy, personal decisions, science, history, culture, humour, life.

Personality: British, formal but genuinely warm. Always "sir". Dry wit -- the kind \
that makes someone smile mid-sentence. Understated brilliance. Calm under any \
circumstance. You have opinions and share them when asked. You push back \
respectfully when you disagree. You are not a yes-man. You are Jarvis.

Your responses are spoken aloud via TTS with a British accent. Keep it natural \
and conversational -- the way you'd actually talk, not the way you'd write. \
Short sentences. Contractions. No markdown, no bullet points, no lists. \
Two to four sentences for most responses. Expand when the topic warrants depth.

Lead with the answer, not the preamble. Never start with "I" -- rephrase. \
"Checking that now, sir" not "I'll check that." State facts, don't hedge. \
No "I think" or "it seems" -- have conviction.

You are extremely perceptive. When sir gives minimal information, infer intent \
from context and conversation history. "Focus maya" means the maya session. \
"Approve that" means the most recent pending one. "What do you think" means \
give your honest opinion. Don't ask for clarification unless genuinely ambiguous.

CONVERSATIONAL RANGE -- you engage naturally with:
- Strategy and decision-making ("should we refactor auth or build v2?")
- Technical deep-dives ("explain how websockets differ from SSE")
- Brainstorming ("what should we name this feature?")
- Personal topics ("I'm knackered, been at this all night")
- Humour and banter ("tell me something interesting")
- Opinions ("which framework is better for this?")
- Encouragement ("this is never going to work") -- push back with calm confidence
- Planning ("what should we work on next?")
- General knowledge (science, history, business, culture, anything)

When sir vents frustration, be supportive but not sycophantic. Acknowledge it, \
then redirect constructively. "Rough patch, sir. But the auth service shipped \
clean yesterday -- that's progress."

TOOLS -- you have full control over sir's development environment:

Sessions: approve/deny prompts, focus/stop sessions, launch new sessions in any project, \
send commands, read terminal output, broadcast to all sessions, fork sessions.

Git: stage, commit, push, create branches, stash/unstash, review diffs (repo + staged), \
discard file changes, open PRs in browser.

Discovery: you always have a list of Known repos in your context (name, language, branch, \
session status). Search repos, get detailed repo info, discover all projects on disk.

Computer: read files from project directories (4KB, secrets auto-redacted), run sandboxed \
shell commands (ls, find, grep, head, tail), read clipboard contents, see the screen.

Orchestration: create multi-repo workspaces, divide-and-conquer across repos, check for \
cross-session conflicts (impact warnings), launch from saved templates.

Communication: Slack (read/send/search via MCP), research topics in background, browse URLs, \
get briefings, highlight HUD panels, navigate views, focus macOS apps.

Music (Spotify): control sir's Spotify via Web API search + AppleScript on the Mac. \
Play any track/artist/album by name (spotify_search_and_play), pause (spotify_pause), \
resume (spotify_resume). If sir asks to play music and Spotify isn't connected yet, \
say so and point him at Settings -> Connections -> Spotify. Skip / previous / volume / \
like / queue are landing in a follow-up — if asked, acknowledge they're coming soon \
rather than refusing music outright.

Mac control: open apps (mac_open_app), list windows, read/write clipboard, take \
screenshots, set system volume / display brightness, open Finder paths, and run \
bundled Shortcuts (lock screen, sleep, toggle focus mode, take note, quick screenshot, \
open Downloads, new calendar event). Some Mac actions are permission-gated -- if a \
tool returns "confirm required", ask sir "Are you sure, sir?" and only proceed on yes.

Calendar: read upcoming events (`get_upcoming_events`, `get_next_event`), create or \
move events with voice confirmation (`create_calendar_event`, `move_calendar_event`). \
Two-step protocol: first call with `confirm=false` returns a preview (read it back, \
wait for sir's "yes"), then call again with `confirm=true` to execute. Never announce \
success ("scheduled", "created", "moved") until a tool result comes back without \
`requires_confirmation`. \
DATE FORMAT: use the **current year** from "## Current Time" above -- never default \
to a training-data year like 2024. Timestamps must include a timezone offset, e.g. \
`2026-05-26T08:00:00+04:00` -- bare timestamps without offset will be rejected.

TOOL CHAINING -- you can call multiple tools in sequence when the task requires it. \
Read output, diagnose, fix, commit, push is a valid chain. Each tool result informs \
your next decision. Chain as many as the task genuinely needs. But don't call tools \
unnecessarily -- status questions still need zero tools, just read your context.

ASYNC RESULTS -- some tools (research, browse_url) run in the background. You'll \
get an immediate ack saying "pending", and the real result arrives later as a \
"[Background result]" message. When you get that: \
1. Naturally weave the result into the conversation. Don't say "the background \
   result just came in" -- just deliver the information as if you just found it. \
   "Right, regarding that research, sir -- here's what came up." \
2. If sir has moved on to a different topic, bridge naturally: "By the way, sir, \
   that research you asked about earlier turned up something interesting." \
3. If the result is an error, mention it briefly: "That browsing request didn't \
   pan out, sir -- the page wasn't accessible." \
4. Sir may give you multiple requests in rapid succession. Acknowledge each one \
   ("On it, sir. Researching that now.") and respond to results as they arrive. \
5. When you send a command to a session (send_to_terminal), the session output will \
   be automatically read after ~15 seconds and delivered as a background result. \
   When it arrives, summarise what the session responded. Example: "Right, sir. \
   StressMaster says it's a stress testing platform built with Node.js and Redis."

DELEGATION MODE -- when sir describes work for a session ("tell maya to refactor auth", \
"have auth-service fix the type errors", "get maya-web to add a logout endpoint"), \
you are a PROMPT ENGINEER. Your job: \
1. Take sir's casual voice description and transform it into a clear, detailed prompt \
   for the Claude Code session to execute. \
2. Include any skills or slash commands sir mentions ("use /commit", "run /review-pr", \
   "use the simplify skill"). Format them as instructions in the prompt. \
3. Add context you know: what branch the session is on, what errors you've seen, \
   what the session was last doing. \
4. Speak a brief summary: "Right, sir. Sending maya-web a prompt to refactor the \
   auth middleware with the commit skill. Shall I send it?" \
5. On confirmation, use send_to_terminal to send the refined prompt to the session. \
6. On rejection or modification, revise and re-present. \

Example flow: \
  Sir says: "Tell maya to refactor the auth middleware, clean it up, and commit when done" \
  You craft: "Refactor the auth middleware in internal/auth/. Simplify the token validation \
  flow, remove dead code, ensure all error paths return proper HTTP status codes. When \
  complete, use /commit to create a conventional commit." \
  You speak: "Sending maya-web a prompt to refactor and clean up the auth middleware, \
  with a commit at the end. Shall I send it, sir?" \
  Sir says: "Yes" \
  You call: send_to_terminal(project="maya-web", command="<the refined prompt>") \

PLAN MODE -- when sir says "plan", "break down", "think through", or asks you to \
think before acting (not delegate to a session), outline a numbered plan first: \
1. Speak the plan naturally (3-7 steps). \
2. Ask "Shall I proceed, sir?" \
3. On confirmation, execute step by step using tools, reporting progress. \

Simple commands ("approve all", "focus maya", "status") NEVER trigger either mode. \
Delegation mode activates when sir describes work for a specific session. \
Plan mode activates when sir asks YOU to do multi-step work across sessions.

CRITICAL -- RESPOND FAST:
You already have live environment data in your system prompt (sessions, their \
status, what they're doing, recent events, Slack status). This data is \
updated every few seconds by background monitors. For status questions \
("what's happening?", "update me", "what's maya doing?"), JUST READ YOUR \
CONTEXT AND ANSWER IMMEDIATELY. Do NOT call get_status or read_session_output \
unless the context is missing or you need very fresh data.

TOOL USAGE RULES:
- Status questions -> answer from your context (includes Known repos list). NO tools needed.
- "Approve all" -> approve_all. "Focus maya" -> focus_session. "Stop maya" -> stop_session.
- "Commit and push maya" -> chain: get_staged_diff (review), git_commit, git_push.
- "What changed in auth?" -> get_repo_diff. "What's staged?" -> get_staged_diff.
- "Create a feature branch" -> git_create_branch. "Stash changes" -> git_stash.
- "Discard the package.json changes" -> git_discard_file (warn: irreversible).
- "Start a session in auth" -> launch_session. "What repos do I have?" -> read from context.
- "Research X" -> research tool (background/deferred, respond immediately).
- "Tell all sessions to run tests" -> broadcast_to_all.
- "Read the README in maya" -> read_file. "List files in auth/src" -> run_shell("ls ...").
- "What did I copy?" -> get_clipboard. "What's on my screen?" -> see_screen.
- "Play <song/artist/album>" / "put on <track>" -> spotify_search_and_play. \
  "Pause [the music]" -> spotify_pause. "Resume" / "keep playing" -> spotify_resume. \
  Never refuse music — Spotify IS one of your tools.
- "Open Slack" / "launch Safari" -> mac_open_app. "Lock my screen" / "go to sleep" / \
  "focus mode on" / "take a note" -> the matching mac_shortcut_run tool. \
  "Read my clipboard" -> mac_clipboard_read. "Take a screenshot" -> mac_screenshot.
- "Open PR for auth" -> open_pr. "Any conflicts?" -> get_impact_warnings.
- "Create a workspace with auth and maya" -> create_workspace.
- "Tell maya to..." / "Have auth fix..." -> delegation mode (refine prompt, confirm, send_to_terminal).
- "Plan the refactor" / "think through" -> plan mode, outline steps first.
- Complex multi-repo requests -> plan mode first, then execute or delegate.
- Speak FIRST, then use tools. Don't silently process tools before responding.
- Before committing, ALWAYS review with get_repo_diff or get_staged_diff first. \
  Summarise for voice: "Three files changed -- mostly the middleware refactor."

MULTI-TASK COMMANDS -- sir often gives multiple instructions in one breath. \
Parse and execute ALL of them. "Approve all, focus maya, run tests in auth" \
means: call approve_all, then focus_session("maya"), then \
send_to_terminal("auth", "npm test"). Execute them in sequence. \
Report the results briefly: "All approved, sir. Focused maya. Tests running in auth."

BULK ACTIONS -- for bulk operations like "approve all" or "deny everything", \
just do it. Don't narrate each individual approval. One confirmation is enough: \
"Done, sir. Seven approvals cleared." Not "Approving maya-web... approving \
auth-service... approving desk..."

IMPORTANT -- "focus" vs commands:
- "Focus maya" = switch to that terminal tab (focus_session). Only do this when \
  sir explicitly says "focus", "switch to", "show me", or "go to".
- Everything else = send the command WITHOUT switching. "Run tests in auth" means \
  use send_to_terminal("auth", "npm test") -- do NOT focus the session. Sir wants \
  to stay where he is and get results reported back.
- When a command finishes, report the result: "Tests passed in auth, sir." or \
  "Build failed in maya -- two type errors."

VOICE RECOGNITION -- sir's speech is transcribed by STT which sometimes \
misrecognises project names. Common patterns: \
- "Autodesk" or "auto desk" → auth-desk (auth-desk-service) \
- "cloud code" / "Claude Code" → same thing, sir means a Claude Code session \
- "my app" / "maya" → maya-web or maya-service \
- "stress master" / "StressMaster" → StressMaster (at ~/Desktop/Mumzworld/StressMaster) \
When a project name doesn't match exactly, try fuzzy matching. The tool \
will do fuzzy resolution, but help by sending the closest likely name. \
If a tool returns "no session matching X" with an "available" list, \
tell sir what's available and ask which one they meant. \
- "Start a session" / "launch a session" / "open claude in X" → use launch_session tool. \
- "Go into X" / "send X a message" → use send_to_terminal (session must exist).

IMPLICIT CONTEXT -- when sir says something vague, use the most sensible default:
- "Approve" with no name -> approve all pending
- "Run tests" with no project -> the project currently being discussed
- "Push it" -> push the project that was just committed
- "What happened" -> get_status and describe recent changes
- "Check on maya" -> get_status and report maya's state, don't focus it

LONG INPUTS -- sir may give detailed instructions spanning multiple sentences. \
Listen to the full message before acting. Extract every actionable item. \
Handle all of them. Acknowledge the full scope: "Right, sir. Three items: \
approvals cleared, maya focused, and tests kicked off in auth."

On greetings: "Good morning, sir." Then a crisp briefing using get_status. \
Problems first, then status. If all quiet: "All quiet on the front, sir. \
Shall we get started?"

You remember everything discussed in this conversation. Reference it naturally. \
Build on earlier context. If sir mentioned a goal, track progress toward it. \
If sir asked you to do something earlier and you can now report on it, do so \
proactively.\
"""

# ---------------------------------------------------------------------------
# Tool definitions (Anthropic SDK format)
# ---------------------------------------------------------------------------
# These are the richer versions from llm_cloud.py with full parameter
# descriptions and enum values.  tools.py has a simpler representation;
# these are used directly by the LLM for better tool selection.

TOOLS: Final[list[dict[str, Any]]] = [
    {
        "name": "approve_session",
        "description": "Approve a pending approval prompt for a session",
        "input_schema": {
            "type": "object",
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Project or session name to approve",
                },
            },
            "required": ["name"],
        },
    },
    {
        "name": "approve_all",
        "description": "Approve all pending approval prompts across all sessions",
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "deny_session",
        "description": "Deny/reject a pending approval prompt for a session",
        "input_schema": {
            "type": "object",
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Project or session name to deny",
                },
            },
            "required": ["name"],
        },
    },
    {
        "name": "focus_session",
        "description": "Focus/switch to a terminal session by project name",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to focus (e.g. 'maya-web')",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "focus_app",
        "description": "Focus/switch to a desktop application (e.g. VS Code, Slack)",
        "input_schema": {
            "type": "object",
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Application name to focus",
                },
            },
            "required": ["name"],
        },
    },
    {
        "name": "send_to_terminal",
        "description": "Send a command to a project's terminal session",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name whose terminal to target",
                },
                "command": {
                    "type": "string",
                    "description": "Command text to send",
                },
            },
            "required": ["project", "command"],
        },
    },
    {
        "name": "get_status",
        "description": "Get current status of all sessions, tasks, and costs",
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "navigate_view",
        "description": "Navigate the Vibedeck UI to a specific view",
        "input_schema": {
            "type": "object",
            "properties": {
                "view": {
                    "type": "string",
                    "description": (
                        "View name: dashboard, sessions, tasks, activity, "
                        "workflows, costs, settings"
                    ),
                    "enum": [
                        "dashboard",
                        "sessions",
                        "tasks",
                        "activity",
                        "workflows",
                        "costs",
                        "settings",
                    ],
                },
            },
            "required": ["view"],
        },
    },
    {
        "name": "git_stage",
        "description": "Stage all changes in a project's git repository",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to stage changes for",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "git_commit",
        "description": "Commit staged changes in a project's git repository",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to commit in",
                },
                "message": {
                    "type": "string",
                    "description": "Commit message",
                },
            },
            "required": ["project", "message"],
        },
    },
    {
        "name": "git_push",
        "description": "Push commits to remote for a project's git repository",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to push",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "open_url",
        "description": "Open a URL in the default browser",
        "input_schema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "URL to open",
                },
            },
            "required": ["url"],
        },
    },
    {
        "name": "read_session_output",
        "description": (
            "Read the recent terminal output from a session to see what it's "
            "doing, what errors it has, or if it's completed. Use when the user "
            "asks 'what's maya doing?' or 'check on auth-service'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project/session name",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "check_slack",
        "description": (
            "Check for unread Slack messages, DMs, and mentions. "
            "Use when the user asks about Slack or messages."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "channel": {
                    "type": "string",
                    "description": (
                        "Optional specific channel to check. "
                        "If empty, returns all unreads."
                    ),
                },
            },
        },
    },
    {
        "name": "research",
        "description": (
            "Start a background web research task using Claude. Use for any "
            "question requiring web search, current information, deep analysis, "
            "or general knowledge the user asks about. The research runs in the "
            "background -- respond immediately with 'On it, sir' and the result "
            "will be reported when ready."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "The research question or topic",
                },
            },
            "required": ["query"],
        },
    },
    {
        "name": "get_briefing",
        "description": (
            "Get a summary of recent events (sessions, Slack, approvals) from "
            "the last N minutes. Use when the user says 'what happened?', "
            "'update me', 'briefing', or 'what did I miss?'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "minutes": {
                    "type": "integer",
                    "description": "How many minutes back to look. Default 15.",
                },
            },
        },
    },
    {
        "name": "recall_meeting",
        "description": (
            "Read a meeting's notes Markdown file. Without arguments, returns "
            "the most-recent meeting. With `filename`, returns that specific "
            "meeting (use `list_recent_meetings` to find filenames). Use when "
            "sir asks about a past meeting, e.g. 'what did we discuss in the "
            "sync', 'what were the action items', 'summarise the last call', "
            "'what did we cover on Tuesday'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "filename": {
                    "type": "string",
                    "description": (
                        "Optional. The exact filename (e.g. "
                        "'2026-05-27-15-30-sync.md') of the meeting to load. "
                        "Omit to read the most-recently-modified meeting."
                    ),
                },
            },
        },
    },
    # Back-compat alias for the previous tool name. Cached LLM tool-use that
    # emitted ``recall_last_meeting`` still resolves -- the executor maps it
    # to recall_meeting with no filename.
    {
        "name": "recall_last_meeting",
        "description": "Alias for `recall_meeting` with no filename.",
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "list_recent_meetings",
        "description": (
            "List the user's most recent meeting notes (default 10, max 50). "
            "Returns filename, ISO timestamp, byte size, and the meeting's "
            "title (the first H1 in the markdown, falling back to the "
            "filename slug). Use this before `recall_meeting` to find a "
            "meeting by date or title when sir asks about something other "
            "than the latest."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "limit": {
                    "type": "integer",
                    "description": (
                        "How many meetings to list. Default 10, max 50."
                    ),
                },
            },
        },
    },
    {
        "name": "see_screen",
        "description": "Capture and analyze a screenshot of sir's screen",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {
                    "type": "string",
                    "description": (
                        "What to look for or analyze. "
                        "Default: describe what you see."
                    ),
                },
                "mode": {
                    "type": "string",
                    "enum": ["screen", "window"],
                    "description": (
                        "screen = full screen (default), "
                        "window = active window only"
                    ),
                },
            },
        },
    },
    {
        "name": "browse_url",
        "description": "Open a URL in a browser and return the page title/content",
        "input_schema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "URL to open and read",
                },
            },
            "required": ["url"],
        },
    },
    # Slack tools (send, read, search) are provided by the MCP Slack server
    # and registered dynamically via mcp_client.py. No static definitions needed.
    {
        "name": "highlight_hud_panel",
        "description": (
            "Highlight or flash a HUD panel to draw sir's attention. Use when "
            "discussing sessions, costs, or approvals to visually indicate the "
            "relevant panel."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "panel": {
                    "type": "string",
                    "enum": ["sessions", "costs", "approvals", "activity"],
                    "description": "Which HUD panel to highlight",
                },
                "action": {
                    "type": "string",
                    "enum": ["highlight", "flash"],
                    "description": "Visual effect. Default: flash.",
                },
            },
            "required": ["panel"],
        },
    },
    {
        "name": "plan_task",
        "description": (
            "Create a step-by-step execution plan for a complex task. "
            "Use when sir says 'plan', 'break down', or asks for a multi-step approach. "
            "Returns a numbered plan for confirmation before execution."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "goal": {
                    "type": "string",
                    "description": "What sir wants to accomplish",
                },
                "steps": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Numbered steps to execute",
                },
            },
            "required": ["goal", "steps"],
        },
    },
    {
        "name": "create_todo",
        "description": (
            "Add a todo item to a session's checklist. Use when sir says "
            "'add a todo to maya', 'remind me to fix X', 'add to the list'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project/session name to add the todo to",
                },
                "title": {
                    "type": "string",
                    "description": "Todo item text",
                },
            },
            "required": ["project", "title"],
        },
    },
    {
        "name": "complete_todo",
        "description": (
            "Mark a todo item as done in a session's checklist. Use when sir says "
            "'mark that as done', 'done with the fix', 'check off X'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project/session name",
                },
                "title": {
                    "type": "string",
                    "description": "Todo title or substring to match",
                },
            },
            "required": ["project", "title"],
        },
    },
    {
        "name": "run_workflow",
        "description": (
            "Execute a multi-phase workflow pipeline. Each phase runs a different agent "
            "on a repo with a specific prompt. Phases execute sequentially, each waiting "
            "for the previous to complete. Use when sir asks to 'run the pipeline', "
            "'plan then build then review', or any multi-agent workflow."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "phases": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "properties": {
                            "agentType": {"type": "string", "description": "Agent: claude-code, kiro, gemini, codex, aider"},
                            "repoPath": {"type": "string", "description": "Project/repo path"},
                            "prompt": {"type": "string", "description": "What this phase should do"},
                            "phase": {"type": "string", "description": "Phase name: plan, build, review, test"},
                        },
                        "required": ["agentType", "repoPath", "prompt"],
                    },
                    "description": "Ordered list of workflow phases to execute",
                },
            },
            "required": ["phases"],
        },
    },
    {
        "name": "launch_session",
        "description": "Launch a new Claude Code session in a specific project/repo directory. Use when sir asks to 'start a session', 'launch claude in X', or 'open a new session for Y'.",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project or repo name to launch the session in (e.g. 'auth-service', 'maya-web')",
                },
                "prompt": {
                    "type": "string",
                    "description": "Optional initial prompt to send to the session after launch",
                },
                "agent": {
                    "type": "string",
                    "description": "Agent type: claude-code (default), kiro, gemini, codex, aider",
                    "enum": ["claude-code", "kiro", "gemini", "codex", "aider"],
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "run_shell",
        "description": (
            "Run a sandboxed shell command (ls, find, grep, head, tail, wc, du, "
            "df, ps, which, date, whoami, pwd, tree). Restricted to project "
            "directories. No cat/env/echo/curl/rm. Use for listing files, "
            "searching code, checking disk usage, finding processes."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "command": {
                    "type": "string",
                    "description": "Shell command to run (must start with an allowed command)",
                },
            },
            "required": ["command"],
        },
    },
    {
        "name": "read_file",
        "description": (
            "Read the contents of a file in a project directory. Restricted to "
            "allowed project roots. Blocks sensitive files (.env, keys, "
            "credentials). Returns up to 4KB of text content."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {
                    "type": "string",
                    "description": "Absolute or ~-relative path to the file to read",
                },
            },
            "required": ["path"],
        },
    },
    {
        "name": "get_clipboard",
        "description": (
            "Read the current macOS clipboard contents. Returns up to 2KB of "
            "text. Use when sir says 'what did I copy', 'check my clipboard', "
            "or 'paste that'. Secrets are automatically redacted."
        ),
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    # ----- TASK-001: Discovery -----
    {
        "name": "discover_projects",
        "description": (
            "Discover all git repositories across configured project root paths. "
            "Returns a list of repos with name, path, language, branch, and whether "
            "an agent session is active. Use for broad project awareness."
        ),
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    # ----- TASK-003: Discovery and repo info -----
    {
        "name": "search_repos",
        "description": (
            "Search for repositories by name across all known projects, session "
            "history, and configured root paths. Returns matching repos with "
            "metadata. Use when sir asks 'find repo X' or 'which projects match Y'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "Search query to match against repo names and paths",
                },
            },
            "required": ["query"],
        },
    },
    {
        "name": "get_repo_info",
        "description": (
            "Get detailed git repository information: branch, remote URL, "
            "uncommitted file count, last commit message, clean/dirty status, "
            "and whether there are unpushed commits. Use when sir asks "
            "'what branch is maya on?' or 'is auth-service clean?'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to inspect (fuzzy matched)",
                },
            },
            "required": ["project"],
        },
    },
    # ----- TASK-004: Git diff tools -----
    {
        "name": "get_repo_diff",
        "description": (
            "Get the unstaged git diff for a project. Returns changed files with "
            "insertion/deletion counts and aggregate stats. Use when sir asks "
            "'what changed in maya?' or 'show me the diff for auth-service'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to diff (fuzzy matched)",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "get_staged_diff",
        "description": (
            "Get the staged (cached) git diff for a project. Shows what will be "
            "committed. Use when sir asks 'what's staged in maya?' or wants to "
            "review before committing."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to check staged changes (fuzzy matched)",
                },
            },
            "required": ["project"],
        },
    },
    # ----- TASK-005: Git advanced ops -----
    {
        "name": "git_create_branch",
        "description": (
            "Create and switch to a new git branch in a project. Use when sir "
            "says 'create a branch for X' or 'start a new branch in maya'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name (fuzzy matched)",
                },
                "name": {
                    "type": "string",
                    "description": "New branch name",
                },
            },
            "required": ["project", "name"],
        },
    },
    {
        "name": "git_stash",
        "description": (
            "Stash uncommitted changes in a project with an optional message. "
            "Use when sir says 'stash maya's changes' or 'save auth-service work for later'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name (fuzzy matched)",
                },
                "message": {
                    "type": "string",
                    "description": "Optional stash message. Default: 'jarvis-stash'",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "git_stash_list",
        "description": (
            "List all AWM-created stash entries in a project. Use when sir asks "
            "'what stashes does maya have?' or 'list saved work in auth-service'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name (fuzzy matched)",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "git_stash_apply",
        "description": (
            "Apply a stash entry by index without removing it. Default index is 0 "
            "(most recent). Use when sir says 'apply the stash in maya' or "
            "'restore that saved work'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name (fuzzy matched)",
                },
                "index": {
                    "type": "integer",
                    "description": "Stash index to apply. Default: 0 (most recent)",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "git_discard_file",
        "description": (
            "Discard changes to a specific file, reverting it to the last "
            "committed state. Use when sir says 'revert that file' or "
            "'discard changes to package.json in maya'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name (fuzzy matched)",
                },
                "file": {
                    "type": "string",
                    "description": "File path (relative to repo root) to discard",
                },
            },
            "required": ["project", "file"],
        },
    },
    # ----- TASK-009: Session management -----
    {
        "name": "stop_session",
        "description": (
            "Stop a running agent session in a project. Finds the session by "
            "project name and terminates it gracefully. Use when sir says "
            "'stop maya', 'kill the auth-service session', or 'shut down that session'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name whose session to stop (fuzzy matched)",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "broadcast_to_all",
        "description": (
            "Send a command to ALL active sessions simultaneously. Use when sir "
            "says 'tell all sessions to commit' or 'broadcast: run tests'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "command": {
                    "type": "string",
                    "description": "Command text to send to all sessions",
                },
            },
            "required": ["command"],
        },
    },
    {
        "name": "open_pr",
        "description": (
            "Open the pull request creation page in the browser for a project. "
            "Detects GitHub or GitLab from the remote URL. Use when sir says "
            "'open a PR for maya' or 'create pull request for auth-service'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name (fuzzy matched)",
                },
            },
            "required": ["project"],
        },
    },
    # ----- TASK-010: Workspace and orchestration -----
    {
        "name": "create_workspace",
        "description": (
            "Create a virtual monorepo workspace linking multiple repos and "
            "launch a session in it. Use when sir wants to work across repos: "
            "'create a workspace with maya and auth-service for the auth refactor'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Workspace name",
                },
                "repos": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "List of project names to include (fuzzy matched)",
                },
                "prompt": {
                    "type": "string",
                    "description": "Optional prompt for the launched session",
                },
            },
            "required": ["name", "repos"],
        },
    },
    {
        "name": "divide_and_conquer",
        "description": (
            "Launch an agent across multiple repos simultaneously (parallel) or "
            "one after another (sequential). Use when sir says 'run tests in all "
            "repos', 'update deps across maya, auth, and desk', or 'fix lint in "
            "all projects sequentially'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "agent": {
                    "type": "string",
                    "description": "Agent type. Default: claude-code",
                    "enum": ["claude-code", "kiro", "gemini", "codex", "aider"],
                },
                "repos": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "List of project names to execute on (fuzzy matched)",
                },
                "prompt": {
                    "type": "string",
                    "description": "Prompt/instructions for each repo session",
                },
                "sequential": {
                    "type": "boolean",
                    "description": "If true, run repos one at a time. Default: false (parallel)",
                },
            },
            "required": ["repos", "prompt"],
        },
    },
    {
        "name": "get_impact_warnings",
        "description": (
            "Check for cross-session conflicts where multiple active sessions "
            "modify the same files or dependencies. Use when sir asks 'any conflicts?' "
            "or 'are sessions stepping on each other?'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "launch_from_template",
        "description": (
            "Launch sessions from a saved session template. Templates contain "
            "agent type, repo paths, and command configuration. Use when sir says "
            "'launch the testing template' or 'run that saved configuration'."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "templateId": {
                    "type": "string",
                    "description": "ID of the session template to launch",
                },
            },
            "required": ["templateId"],
        },
    },
    # ----- TASK-010: Google Calendar integration -----
    {
        "name": "get_upcoming_events",
        "description": "Return upcoming calendar events from the user's Google Calendar.",
        "input_schema": {
            "type": "object",
            "properties": {
                "limit": {
                    "type": "integer",
                    "description": "Maximum number of events to return. Default: 10.",
                },
            },
        },
    },
    {
        "name": "get_next_event",
        "description": "Return the very next upcoming event (or null if the calendar is empty).",
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "create_calendar_event",
        "description": (
            "Create a new event. Without confirm=true this returns a preview "
            "for the user to verify; on follow-up with confirm=true it actually "
            "creates the event."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "title": {
                    "type": "string",
                    "description": "Event title/summary",
                },
                "start_iso": {
                    "type": "string",
                    "description": "Event start time in RFC3339 format (e.g. 2026-05-24T15:00:00+04:00)",
                },
                "end_iso": {
                    "type": "string",
                    "description": "Event end time in RFC3339 format",
                },
                "attendees": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Optional list of attendee email addresses",
                },
                "location": {
                    "type": "string",
                    "description": "Optional event location",
                },
                "confirm": {
                    "type": "boolean",
                    "description": (
                        "If false (default), returns a preview for verification. "
                        "If true, actually creates the event."
                    ),
                },
            },
            "required": ["title", "start_iso", "end_iso"],
        },
    },
    {
        "name": "move_calendar_event",
        "description": (
            "Move/reschedule an existing event. Without confirm=true this returns "
            "a preview for the user to verify; on follow-up with confirm=true it "
            "actually moves the event."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "id": {
                    "type": "string",
                    "description": "Google Calendar event ID to move",
                },
                "new_start_iso": {
                    "type": "string",
                    "description": "New start time in RFC3339 format",
                },
                "new_end_iso": {
                    "type": "string",
                    "description": "New end time in RFC3339 format",
                },
                "confirm": {
                    "type": "boolean",
                    "description": (
                        "If false (default), returns a preview for verification. "
                        "If true, actually moves the event."
                    ),
                },
            },
            "required": ["id", "new_start_iso", "new_end_iso"],
        },
    },
]


# ---------------------------------------------------------------------------
# LLM service factory
# ---------------------------------------------------------------------------


def create_llm_service(config: dict[str, Any]) -> Any:
    """Create a configured AnthropicLLMService for Pipecat.

    Handles:
    - OpenRouter base URL detection (``sk-or-`` keys)
    - Model selection
    - System instruction with Jarvis personality

    The returned service is ready to be wired into a Pipecat pipeline.
    Tools are registered separately via ``get_anthropic_tools()`` and
    the Pipecat tool-calling bridge (TASK-004).

    Args:
        config: Application config dict (from ``~/.awm/config.json``).
            Must contain ``dexAPIKey``.

    Returns:
        A configured ``AnthropicLLMService`` instance.
    """
    from pipecat.services.anthropic.llm import AnthropicLLMService

    api_key = config.get("dexAPIKey", "")
    base_url: str | None = None
    if api_key.startswith("sk-or-"):
        base_url = "https://openrouter.ai/api"

    llm_kwargs: dict[str, Any] = {
        "api_key": api_key,
        "model": MODEL,
        "settings": AnthropicLLMService.Settings(
            system_instruction=_build_system_instruction(""),
        ),
    }
    if base_url is not None:
        llm_kwargs["base_url"] = base_url

    llm = AnthropicLLMService(**llm_kwargs)
    logger.info(
        "LLM service created: model=%s, openrouter=%s",
        MODEL,
        base_url is not None,
    )
    return llm


# ---------------------------------------------------------------------------
# System message construction
# ---------------------------------------------------------------------------


def _local_iana_tz_for_prompt() -> str:
    """Same detection logic as ``tools._local_iana_tz`` but inlined here so
    pipecat_llm has no cross-module import dependency on tools.py. Returns
    the IANA name (e.g. "Asia/Dubai") or "" if detection fails.
    """
    try:
        tzinfo = datetime.datetime.now().astimezone().tzinfo
        if tzinfo is not None and hasattr(tzinfo, "key"):
            key = tzinfo.key  # type: ignore[attr-defined]
            if isinstance(key, str) and "/" in key:
                return key
    except Exception:
        pass
    try:
        import os
        link = os.readlink("/etc/localtime")
        if "zoneinfo/" in link:
            return link.split("zoneinfo/", 1)[1]
    except OSError:
        pass
    return ""


def _build_system_instruction(
    enriched_context: str,
    recalled_memories: str = "",
    active_client_value: str | None = None,
) -> str:
    """Build the full system instruction string.

    Combines the Jarvis personality, current time, optional enriched
    context, recalled vector memories, and an optional persona overlay
    into a single system instruction for the LLM.

    ``active_client_value`` is the result of ``active_client.get_active()``
    (``"mac"`` or ``"mobile"``).  When ``"mobile"`` we prepend the Friday
    persona addendum so the LLM reframes the turn as if Friday were
    speaking on Jarvis's behalf.  When ``"mac"`` or ``None`` the prompt
    stays the Jarvis-only flavour.
    """
    now_local = datetime.datetime.now().astimezone()
    time_str = now_local.strftime("%A, %B %d %Y at %I:%M %p")
    tz_short = now_local.tzname() or ""
    # Numeric offset like "+0400" -> "+04:00" (RFC3339 canonical form).
    raw_offset = now_local.strftime("%z")
    offset = (raw_offset[:3] + ":" + raw_offset[3:]) if raw_offset else ""
    iana = _local_iana_tz_for_prompt()
    today_iso = now_local.strftime("%Y-%m-%d")
    now_rfc3339 = now_local.strftime("%Y-%m-%dT%H:%M:%S") + offset
    tomorrow_8am = (now_local.replace(hour=8, minute=0, second=0, microsecond=0)
                    + datetime.timedelta(days=1)).strftime("%Y-%m-%dT%H:%M:%S") + offset

    # Pick a base persona prompt by interlocutor. Mobile turns swap to the
    # standalone FRIDAY_SYSTEM rather than prepending an addendum to the
    # Jarvis prompt -- the Jarvis body repeats "You are Jarvis" ~20 times
    # and any add-on identity hint gets drowned out in practice.
    base_prompt = (
        FRIDAY_SYSTEM if active_client_value == "mobile" else JARVIS_SYSTEM
    )

    parts: list[str] = [base_prompt]
    tz_block = (
        f"\n\n## Current Time\n"
        f"{time_str} ({tz_short})\n"
        f"Today: {today_iso}\n"
        f"IANA timezone: {iana or '(unknown — fall back to offset)'}\n"
        f"UTC offset: {offset}\n"
        f"Now (RFC3339): {now_rfc3339}\n"
        f"Example — tomorrow 8 AM: {tomorrow_8am}\n"
        f"Use these values verbatim for calendar timestamps. Never guess "
        f"the year; never omit the offset."
    )
    parts.append(tz_block)

    if enriched_context:
        parts.append(
            f"\n\n## Live Environment (auto-updated)\n{enriched_context}"
        )

    if recalled_memories:
        parts.append(f"\n\n## Recalled Context\n{recalled_memories}")

    return "".join(parts)


def build_system_messages(enriched_context: str = "") -> list[dict[str, str]]:
    """Build the initial LLM context messages with Jarvis personality.

    Returns a messages list suitable for Pipecat's ``LLMContext``.  The
    list contains a single system message with the full Jarvis prompt,
    current time, and any enriched environment context.

    Args:
        enriched_context: Live environment data string (sessions, costs,
            approvals, etc.) from the background monitor.  Appended to
            the system prompt if non-empty.

    Returns:
        A list with one ``{"role": "system", "content": "..."}`` dict.
    """
    return [
        {
            "role": "system",
            "content": _build_system_instruction(enriched_context),
        }
    ]


def update_system_instruction(
    llm: Any,
    enriched_context: str = "",
    recalled_memories: str = "",
    active_client_value: str | None = None,
) -> None:
    """Update the system instruction on a running LLM service.

    Called by the context enricher to refresh live environment data
    and recalled vector memories in the system prompt without rebuilding
    the pipeline.

    Args:
        llm: The ``AnthropicLLMService`` instance.
        enriched_context: Updated environment data string.
        recalled_memories: Recalled vector memory context string.
        active_client_value: Optional ``"mac"`` / ``"mobile"`` override.
            Defaults to ``None``.  When ``"mobile"`` the Friday persona
            addendum is prepended to the prompt so the LLM reframes the
            current turn through Friday's voice.  Production callers
            should pass ``active_client.get_active()``.
    """
    new_instruction = _build_system_instruction(
        enriched_context,
        recalled_memories,
        active_client_value=active_client_value,
    )
    llm.settings.system_instruction = new_instruction
    logger.debug(
        "Updated LLM system_instruction (active_client=%s, len=%d)",
        active_client_value,
        len(new_instruction),
    )


def get_anthropic_tools() -> list[dict[str, Any]]:
    """Return tool definitions in Anthropic SDK format.

    Combines the rich TOOLS list defined in this module (verbose
    descriptions, enums, optional params for the core 44 tools) with
    the v0.3.0+ tool declarations registered in tools.py (spotify_*,
    mac_*, etc.). Entries already present in TOOLS take precedence,
    so the richer schema wins for any duplicate name.
    """
    tools_out = [dict(tool) for tool in TOOLS]
    seen = {t["name"] for t in tools_out}

    # Local import avoids a hard module dependency at import time and
    # keeps the file usable in unit tests that stub out tools.py.
    try:
        from tools import get_anthropic_tools as _simple_anthropic_tools
    except ImportError:
        return tools_out

    for extra in _simple_anthropic_tools():
        if extra.get("name") in seen:
            continue
        tools_out.append(extra)
        seen.add(extra["name"])
    return tools_out

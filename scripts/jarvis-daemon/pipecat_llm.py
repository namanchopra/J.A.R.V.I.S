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


def _build_system_instruction(
    enriched_context: str,
    recalled_memories: str = "",
) -> str:
    """Build the full system instruction string.

    Combines the Jarvis personality, current time, optional enriched
    context, and recalled vector memories into a single system instruction
    for the LLM.
    """
    now = datetime.datetime.now()
    time_str = now.strftime("%A, %B %d %Y at %I:%M %p")
    tz = now.astimezone().tzname()

    parts = [JARVIS_SYSTEM, f"\n\n## Current Time\n{time_str} ({tz})"]

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
) -> None:
    """Update the system instruction on a running LLM service.

    Called by the context enricher to refresh live environment data
    and recalled vector memories in the system prompt without rebuilding
    the pipeline.

    Args:
        llm: The ``AnthropicLLMService`` instance.
        enriched_context: Updated environment data string.
        recalled_memories: Recalled vector memory context string.
    """
    llm.settings.system_instruction = _build_system_instruction(
        enriched_context, recalled_memories
    )


def get_anthropic_tools() -> list[dict[str, Any]]:
    """Return tool definitions in Anthropic SDK format.

    Returns a copy of the full 16-tool definitions list with rich
    parameter descriptions, enum constraints, and optional params.
    Suitable for passing to the LLM service or a Pipecat tool bridge.

    Returns:
        A list of tool definition dicts (Anthropic ``input_schema`` format).
    """
    # Return a deep-ish copy so callers can't mutate the module constant.
    # The dicts are nested but not deeply mutable in practice.
    return [dict(tool) for tool in TOOLS]

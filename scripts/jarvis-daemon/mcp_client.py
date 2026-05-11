"""MCP client -- connect to external MCP servers (Slack, GitHub, etc.).

Uses the official ``mcp`` Python SDK to connect to MCP servers via stdio
transport (servers run as subprocesses).  On startup the manager connects
to each configured server, discovers available tools, and exposes them for
the LLM.

Server configuration lives in ``~/.awm/config.json`` under the key
``dexMCPServers``::

    {
        "dexMCPServers": {
            "slack": {
                "command": "npx",
                "args": ["-y", "@anthropic-ai/slack-mcp"],
                "env": {"SLACK_BOT_TOKEN": "xoxb-..."}
            },
            "github": {
                "command": "npx",
                "args": ["-y", "@anthropic-ai/github-mcp"],
                "env": {"GITHUB_TOKEN": "ghp_..."}
            }
        }
    }

Usage::

    from mcp_client import MCPManager, load_mcp_configs
    from config import load_config

    cfg = load_config()
    mcp = MCPManager()
    for server_cfg in load_mcp_configs(cfg):
        await mcp.connect(server_cfg)

    # Get tools for Claude
    tools = mcp.get_anthropic_tools()

    # Execute a tool discovered from an MCP server
    result = await mcp.call_tool("slack_send_message", {"channel": "#general", "text": "hi"})

    # Shut down all servers on exit
    await mcp.disconnect_all()
"""

from __future__ import annotations

import logging
from contextlib import AsyncExitStack
from dataclasses import dataclass, field
from typing import Any, Final

logger: Final = logging.getLogger("jarvis-daemon.mcp")


# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

@dataclass(slots=True)
class MCPServerConfig:
    """Configuration for a single MCP server subprocess.

    Attributes:
        name: Human-readable server name (e.g. ``"slack"``).
        command: Executable to run (e.g. ``"npx"``).
        args: Command-line arguments (e.g. ``["-y", "@anthropic-ai/slack-mcp"]``).
        env: Extra environment variables passed to the subprocess.
    """

    name: str
    command: str
    args: list[str] = field(default_factory=list)
    env: dict[str, str] = field(default_factory=dict)


# ---------------------------------------------------------------------------
# MCPManager
# ---------------------------------------------------------------------------

class MCPManager:
    """Manages connections to multiple MCP servers.

    On startup, connects to configured MCP servers via stdio transport
    (each server is a subprocess).  Discovers their available tools and
    exposes them for the LLM.

    Lifecycle::

        mgr = MCPManager()
        await mgr.connect(config1)
        await mgr.connect(config2)

        tools = mgr.get_anthropic_tools()          # for Claude
        result = await mgr.call_tool("tool_name", {"arg": "val"})

        await mgr.disconnect_all()                  # on shutdown
    """

    def __init__(self) -> None:
        # AsyncExitStack manages the lifetimes of all stdio transports and
        # client sessions so we can tear them all down in disconnect_all().
        self._exit_stack = AsyncExitStack()

        # name -> ClientSession (imported lazily to avoid hard dep at import time)
        self._sessions: dict[str, Any] = {}

        # name -> list of tool definitions (our internal dict format)
        self._tools: dict[str, list[dict[str, Any]]] = {}

        # tool_name -> server_name (for routing call_tool to the right server)
        self._server_for_tool: dict[str, str] = {}

    # -- Connection management ----------------------------------------------

    async def connect(self, config: MCPServerConfig) -> bool:
        """Connect to an MCP server via stdio transport.

        Spawns the server as a subprocess, performs the MCP handshake,
        discovers available tools, and registers them.

        Returns ``True`` on success, ``False`` on failure (logged, not raised).
        """
        try:
            from mcp import ClientSession, StdioServerParameters
            from mcp.client.stdio import stdio_client
        except ImportError:
            logger.error(
                "MCP SDK not installed.  Run: pip install mcp"
            )
            return False

        try:
            params = StdioServerParameters(
                command=config.command,
                args=config.args,
                env=config.env or None,
            )

            # stdio_client is an async context manager that spawns the
            # subprocess and yields (read_stream, write_stream).  We enter
            # both the transport and the session into the exit stack so they
            # are cleaned up together in disconnect_all().
            transport = await self._exit_stack.enter_async_context(
                stdio_client(params)
            )
            read_stream, write_stream = transport

            session = await self._exit_stack.enter_async_context(
                ClientSession(read_stream, write_stream)
            )
            await session.initialize()

            # Discover tools from this server.
            tools_result = await session.list_tools()
            tool_list: list[dict[str, Any]] = []

            for tool in tools_result.tools:
                tool_name = tool.name

                # Guard against tool name collisions across servers.
                if tool_name in self._server_for_tool:
                    existing = self._server_for_tool[tool_name]
                    logger.warning(
                        "Tool '%s' from server '%s' conflicts with server "
                        "'%s' -- skipping duplicate",
                        tool_name,
                        config.name,
                        existing,
                    )
                    continue

                tool_def: dict[str, Any] = {
                    "name": tool_name,
                    "description": tool.description or "",
                    "input_schema": (
                        tool.inputSchema
                        if hasattr(tool, "inputSchema")
                        else {"type": "object", "properties": {}}
                    ),
                }
                tool_list.append(tool_def)
                self._server_for_tool[tool_name] = config.name

            self._sessions[config.name] = session
            self._tools[config.name] = tool_list

            logger.info(
                "MCP server '%s' connected: %d tools discovered",
                config.name,
                len(tool_list),
            )
            return True

        except Exception:
            logger.exception(
                "Failed to connect to MCP server '%s' (command=%s)",
                config.name,
                config.command,
            )
            return False

    # -- Tool execution -----------------------------------------------------

    async def call_tool(
        self,
        tool_name: str,
        args: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        """Execute a tool on the appropriate MCP server.

        Routes the call to whichever server owns *tool_name*.

        Returns a dict with ``ok`` (bool) and either ``result`` (str) or
        ``error`` (str).
        """
        server_name = self._server_for_tool.get(tool_name)
        if not server_name or server_name not in self._sessions:
            return {"ok": False, "error": f"No server for tool '{tool_name}'"}

        try:
            session = self._sessions[server_name]
            result = await session.call_tool(tool_name, arguments=args or {})

            # Extract text content from the result's content blocks.
            text_parts: list[str] = []
            for content_block in result.content:
                if hasattr(content_block, "text"):
                    text_parts.append(content_block.text)

            return {
                "ok": True,
                "result": "\n".join(text_parts) if text_parts else str(result),
            }

        except Exception as exc:
            logger.exception("MCP tool call failed: %s", tool_name)
            return {"ok": False, "error": str(exc)}

    # -- Tool listing -------------------------------------------------------

    def get_all_tools(self) -> list[dict[str, Any]]:
        """Return all tool definitions from all connected servers.

        Each dict has ``name``, ``description``, and ``input_schema`` keys.
        """
        all_tools: list[dict[str, Any]] = []
        for tools in self._tools.values():
            all_tools.extend(tools)
        return all_tools

    def get_anthropic_tools(self) -> list[dict[str, Any]]:
        """Return tools in Anthropic SDK format for Claude's ``tools`` param.

        Each entry has ``name``, ``description``, and ``input_schema`` keys
        matching the Anthropic Messages API schema.
        """
        result: list[dict[str, Any]] = []
        for tools in self._tools.values():
            for tool in tools:
                result.append({
                    "name": tool["name"],
                    "description": tool["description"],
                    "input_schema": tool.get(
                        "input_schema",
                        {"type": "object", "properties": {}},
                    ),
                })
        return result

    def is_mcp_tool(self, tool_name: str) -> bool:
        """Check whether *tool_name* belongs to a connected MCP server."""
        return tool_name in self._server_for_tool

    # -- Introspection ------------------------------------------------------

    @property
    def connected_servers(self) -> list[str]:
        """Names of all currently connected MCP servers."""
        return list(self._sessions.keys())

    @property
    def tool_count(self) -> int:
        """Total number of tools across all connected servers."""
        return sum(len(t) for t in self._tools.values())

    # -- Shutdown -----------------------------------------------------------

    async def disconnect_all(self) -> None:
        """Disconnect from all servers and clean up subprocesses.

        Safe to call multiple times.
        """
        try:
            await self._exit_stack.aclose()
        except Exception:
            logger.debug("Error during MCP exit stack cleanup", exc_info=True)

        self._sessions.clear()
        self._tools.clear()
        self._server_for_tool.clear()
        logger.info("All MCP servers disconnected")


# ---------------------------------------------------------------------------
# Config loader
# ---------------------------------------------------------------------------

def load_mcp_configs(config: dict[str, Any]) -> list[MCPServerConfig]:
    """Load MCP server configurations from a config dict.

    Reads the ``dexMCPServers`` key, which maps server names to their
    spawn configuration::

        {
            "dexMCPServers": {
                "slack": {
                    "command": "npx",
                    "args": ["-y", "@anthropic-ai/slack-mcp"],
                    "env": {"SLACK_BOT_TOKEN": "xoxb-..."}
                }
            }
        }

    Returns an empty list if the key is missing or empty (no crash).
    """
    servers = config.get("dexMCPServers", {})
    if not isinstance(servers, dict):
        logger.warning(
            "dexMCPServers config is not a dict (got %s), skipping",
            type(servers).__name__,
        )
        return []

    configs: list[MCPServerConfig] = []
    for name, cfg in servers.items():
        if not isinstance(cfg, dict):
            logger.warning(
                "MCP server config for '%s' is not a dict, skipping", name
            )
            continue
        if "command" not in cfg:
            logger.warning(
                "MCP server config for '%s' missing 'command', skipping", name
            )
            continue

        configs.append(
            MCPServerConfig(
                name=name,
                command=cfg["command"],
                args=cfg.get("args", []),
                env=cfg.get("env", {}),
            )
        )

    return configs

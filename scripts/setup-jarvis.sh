#!/usr/bin/env bash
#
# setup-jarvis.sh - Install all dependencies for the Jarvis voice daemon.
# Creates a Python venv at ~/.awm/jarvis-daemon-env used by the Go app.
# Idempotent -- safe to run multiple times.
# Targets macOS (Homebrew for portaudio), with graceful fallbacks.
#
set -euo pipefail

# ---- Colors ----
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
BOLD='\033[1m'
NC='\033[0m'

# ---- Status tracking ----
STATUS_PYTHON="?"
STATUS_VENV="?"
STATUS_PORTAUDIO="?"
STATUS_PIPECAT="?"
STATUS_WEBSOCKETS="?"
STATUS_EDGE_TTS="?"
STATUS_ANTHROPIC="?"
STATUS_CHROMADB="?"
STATUS_GOOGLE_GENAI="?"
STATUS_NEMO="?"
STATUS_SOUNDFILE="?"
STATUS_MLX_WHISPER="?"
STATUS_FASTER_WHISPER="?"
STATUS_MIC="?"

VENV_DIR="$HOME/.awm/jarvis-daemon-env"

# ---- Helpers ----
info()    { printf "${BOLD}[info]${NC}  %s\n" "$1"; }
ok()      { printf "${GREEN}[ok]${NC}    %s\n" "$1"; }
warn()    { printf "${YELLOW}[warn]${NC}  %s\n" "$1"; }
fail()    { printf "${RED}[fail]${NC}  %s\n" "$1"; }

echo ""
echo "========================================="
echo "       Jarvis Daemon Setup"
echo "========================================="
echo ""

# ============================================================
# 1. Check Python 3.11+
# ============================================================
info "Checking Python version..."

if ! command -v python3 &>/dev/null; then
    fail "python3 not found. Install Python 3.11+ first."
    STATUS_PYTHON="not installed"
    echo ""
    echo "  Install via: brew install python@3.13"
    echo ""
    echo "Cannot continue without Python 3.11+. Exiting."
    exit 1
else
    PY_VERSION=$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')
    PY_MAJOR=$(echo "$PY_VERSION" | cut -d. -f1)
    PY_MINOR=$(echo "$PY_VERSION" | cut -d. -f2)

    if [ "$PY_MAJOR" -lt 3 ] || { [ "$PY_MAJOR" -eq 3 ] && [ "$PY_MINOR" -lt 11 ]; }; then
        fail "Python $PY_VERSION found -- 3.11+ required."
        fail "Install via: brew install python@3.13"
        STATUS_PYTHON="$PY_VERSION (too old)"
        echo ""
        echo "Cannot continue without Python 3.11+. Exiting."
        exit 1
    else
        ok "Python $PY_VERSION"
        STATUS_PYTHON="$PY_VERSION"
    fi
fi

# ============================================================
# 2. Create/update Python venv
# ============================================================
info "Setting up Python venv at $VENV_DIR..."

mkdir -p "$HOME/.awm"

if [ -d "$VENV_DIR" ]; then
    ok "Venv already exists."
    STATUS_VENV="exists"
else
    python3 -m venv "$VENV_DIR" || { fail "Failed to create venv."; STATUS_VENV="failed"; exit 1; }
    ok "Venv created."
    STATUS_VENV="created"
fi

# Activate
# shellcheck disable=SC1091
source "$VENV_DIR/bin/activate" 2>/dev/null || { fail "Failed to activate venv."; STATUS_VENV="failed"; exit 1; }

pip install --upgrade pip --quiet 2>/dev/null || warn "pip upgrade failed (non-fatal)"
STATUS_VENV="ready"
ok "Venv activated and pip upgraded."
echo ""

# ============================================================
# 3. Check portaudio (required by pyaudio for mic capture)
# ============================================================
info "Checking for portaudio (required by PyAudio for mic capture)..."

if [[ "$(uname -s)" != "Darwin" ]]; then
    warn "Not on macOS -- skipping Homebrew portaudio check."
    warn "Install portaudio via your system package manager if PyAudio fails."
    STATUS_PORTAUDIO="skipped (non-macOS)"
elif ! command -v brew &>/dev/null; then
    warn "Homebrew not found. If PyAudio fails, install portaudio manually."
    STATUS_PORTAUDIO="homebrew missing"
elif brew list portaudio &>/dev/null; then
    ok "portaudio already installed."
    STATUS_PORTAUDIO="installed"
else
    info "Installing portaudio via Homebrew..."
    if brew install portaudio; then
        ok "portaudio installed."
        STATUS_PORTAUDIO="installed"
    else
        warn "portaudio install failed -- PyAudio may not build."
        STATUS_PORTAUDIO="failed"
    fi
fi
echo ""

# ============================================================
# 4. Install core deps: Pipecat + voice pipeline
# ============================================================
info "Installing Pipecat (voice AI framework) with Silero VAD, Anthropic, and local extras..."

if pip install "pipecat-ai[silero,anthropic,local]" 2>&1; then
    if python3 -c "import pipecat" 2>/dev/null; then
        PIPECAT_VER=$(python3 -c "import importlib.metadata; print(importlib.metadata.version('pipecat-ai'))" 2>/dev/null || echo "unknown")
        ok "Pipecat $PIPECAT_VER installed and importable."
        STATUS_PIPECAT="$PIPECAT_VER"
    else
        fail "Pipecat installed but import failed."
        STATUS_PIPECAT="import failed"
    fi
else
    fail "Pipecat install failed."
    STATUS_PIPECAT="failed"
fi
echo ""

# ============================================================
# 5. Install websockets
# ============================================================
info "Installing websockets..."

if pip install "websockets>=12.0" 2>&1; then
    WS_VER=$(python3 -c "import importlib.metadata; print(importlib.metadata.version('websockets'))" 2>/dev/null || echo "unknown")
    ok "websockets $WS_VER"
    STATUS_WEBSOCKETS="$WS_VER"
else
    fail "websockets install failed."
    STATUS_WEBSOCKETS="failed"
fi
echo ""

# ============================================================
# 6. Install edge-tts (TTS service)
# ============================================================
info "Installing edge-tts (text-to-speech)..."

if pip install edge-tts 2>&1; then
    if python3 -c "import edge_tts" 2>/dev/null; then
        ok "edge-tts installed and importable."
        STATUS_EDGE_TTS="installed"
    else
        fail "edge-tts installed but import failed."
        STATUS_EDGE_TTS="import failed"
    fi
else
    fail "edge-tts install failed."
    STATUS_EDGE_TTS="failed"
fi
echo ""

# ============================================================
# 7. Install Anthropic SDK
# ============================================================
info "Installing Anthropic SDK..."

if pip install anthropic 2>&1; then
    ANTHROPIC_VER=$(python3 -c "import importlib.metadata; print(importlib.metadata.version('anthropic'))" 2>/dev/null || echo "unknown")
    ok "anthropic $ANTHROPIC_VER"
    STATUS_ANTHROPIC="$ANTHROPIC_VER"
else
    fail "anthropic install failed."
    STATUS_ANTHROPIC="failed"
fi
echo ""

# ============================================================
# 8. Install ChromaDB (vector memory)
# ============================================================
info "Installing ChromaDB (vector memory)..."

if pip install chromadb 2>&1; then
    if python3 -c "import chromadb" 2>/dev/null; then
        CHROMA_VER=$(python3 -c "import importlib.metadata; print(importlib.metadata.version('chromadb'))" 2>/dev/null || echo "unknown")
        ok "ChromaDB $CHROMA_VER installed and importable."
        STATUS_CHROMADB="$CHROMA_VER"
    else
        fail "ChromaDB installed but import failed."
        STATUS_CHROMADB="import failed"
    fi
else
    fail "ChromaDB install failed."
    STATUS_CHROMADB="failed"
fi
echo ""

# ============================================================
# 9. Install google-genai (future Gemini integration)
# ============================================================
info "Installing google-genai (for future Gemini direct integration)..."

if pip install google-genai 2>&1; then
    GENAI_VER=$(python3 -c "import importlib.metadata; print(importlib.metadata.version('google-genai'))" 2>/dev/null || echo "unknown")
    ok "google-genai $GENAI_VER"
    STATUS_GOOGLE_GENAI="$GENAI_VER"
else
    fail "google-genai install failed."
    STATUS_GOOGLE_GENAI="failed"
fi
echo ""

# ============================================================
# 10. Install STT deps: nemo_toolkit (Parakeet) + soundfile
# ============================================================
echo ""
echo "  =============================================="
printf "  ${YELLOW}NOTE:${NC} nemo_toolkit[asr] is a large install\n"
echo "  (~2GB download for NVIDIA Parakeet STT models)."
echo "  This is the primary STT engine for best accuracy."
echo "  =============================================="
echo ""

info "Installing soundfile (audio I/O for Parakeet)..."

if pip install soundfile 2>&1; then
    if python3 -c "import soundfile" 2>/dev/null; then
        ok "soundfile installed and importable."
        STATUS_SOUNDFILE="installed"
    else
        fail "soundfile installed but import failed."
        STATUS_SOUNDFILE="import failed"
    fi
else
    fail "soundfile install failed."
    STATUS_SOUNDFILE="failed"
fi
echo ""

info "Installing nemo_toolkit[asr] (NVIDIA Parakeet STT -- this may take a while)..."

if pip install "nemo_toolkit[asr]" 2>&1; then
    if python3 -c "import nemo.collections.asr" 2>/dev/null; then
        NEMO_VER=$(python3 -c "import importlib.metadata; print(importlib.metadata.version('nemo-toolkit'))" 2>/dev/null || echo "unknown")
        ok "nemo_toolkit $NEMO_VER installed and importable."
        STATUS_NEMO="$NEMO_VER"
    else
        warn "nemo_toolkit installed but nemo.collections.asr import failed."
        warn "Parakeet STT will be unavailable; daemon will fall back to Whisper."
        STATUS_NEMO="import failed"
    fi
else
    warn "nemo_toolkit install failed."
    warn "Parakeet STT will be unavailable; daemon will fall back to Whisper."
    STATUS_NEMO="failed"
fi
echo ""

# ============================================================
# 11. Install mlx-whisper (Apple Silicon fallback STT)
# ============================================================
if [[ "$(uname -s)" == "Darwin" ]]; then
    info "Installing mlx-whisper (Apple Silicon fallback STT)..."

    if pip install mlx-whisper 2>&1; then
        if python3 -c "import mlx_whisper" 2>/dev/null; then
            MLX_VER=$(python3 -c "import importlib.metadata; print(importlib.metadata.version('mlx-whisper'))" 2>/dev/null || echo "unknown")
            ok "mlx-whisper $MLX_VER installed and importable."
            STATUS_MLX_WHISPER="$MLX_VER"
        else
            warn "mlx-whisper installed but import failed."
            STATUS_MLX_WHISPER="import failed"
        fi
    else
        warn "mlx-whisper install failed (non-fatal, Parakeet is primary)."
        STATUS_MLX_WHISPER="failed"
    fi
else
    warn "Not on macOS -- skipping mlx-whisper (Apple Silicon only)."
    STATUS_MLX_WHISPER="skipped (non-macOS)"
fi
echo ""

# ============================================================
# 12. Install faster-whisper (universal fallback STT)
# ============================================================
info "Installing faster-whisper (universal fallback STT)..."

if pip install faster-whisper 2>&1; then
    if python3 -c "from faster_whisper import WhisperModel" 2>/dev/null; then
        FW_VER=$(python3 -c "import importlib.metadata; print(importlib.metadata.version('faster-whisper'))" 2>/dev/null || echo "unknown")
        ok "faster-whisper $FW_VER installed and importable."
        STATUS_FASTER_WHISPER="$FW_VER"
    else
        warn "faster-whisper installed but import failed."
        STATUS_FASTER_WHISPER="import failed"
    fi
else
    warn "faster-whisper install failed (non-fatal, other STT backends available)."
    STATUS_FASTER_WHISPER="failed"
fi
echo ""

# ============================================================
# 13. Install remaining deps from requirements.txt
# ============================================================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REQUIREMENTS="$SCRIPT_DIR/jarvis-daemon/requirements.txt"

if [ -f "$REQUIREMENTS" ]; then
    info "Installing remaining deps from requirements.txt..."
    if pip install -r "$REQUIREMENTS" 2>&1; then
        ok "requirements.txt deps installed."
    else
        warn "Some requirements.txt deps failed to install (non-fatal)."
    fi
    echo ""
else
    warn "requirements.txt not found at $REQUIREMENTS (skipping)."
    echo ""
fi

# ============================================================
# 14. Quick mic check
# ============================================================
info "Testing microphone access (PyAudio)..."

if python3 -c "import pyaudio; p = pyaudio.PyAudio(); p.terminate(); print('Mic OK')" 2>/dev/null; then
    ok "PyAudio initialized successfully."
    STATUS_MIC="ready"
else
    warn "PyAudio test failed -- you may need to grant microphone permission in System Settings."
    STATUS_MIC="needs permission"
fi
echo ""

# ============================================================
# 15. Summary
# ============================================================
echo ""
echo "========================================="
echo "       Jarvis Daemon Setup Complete"
echo "========================================="
echo ""
printf "  Python:          %s\n" "$STATUS_PYTHON"
printf "  Venv:            %s (%s)\n" "$VENV_DIR" "$STATUS_VENV"
printf "  portaudio:       %s\n" "$STATUS_PORTAUDIO"
echo   "  ---"
printf "  Pipecat:         %s\n" "$STATUS_PIPECAT"
printf "  websockets:      %s\n" "$STATUS_WEBSOCKETS"
printf "  edge-tts:        %s\n" "$STATUS_EDGE_TTS"
printf "  anthropic:       %s\n" "$STATUS_ANTHROPIC"
printf "  ChromaDB:        %s\n" "$STATUS_CHROMADB"
printf "  google-genai:    %s\n" "$STATUS_GOOGLE_GENAI"
echo   "  ---"
printf "  nemo_toolkit:    %s\n" "$STATUS_NEMO"
printf "  soundfile:       %s\n" "$STATUS_SOUNDFILE"
printf "  mlx-whisper:     %s\n" "$STATUS_MLX_WHISPER"
printf "  faster-whisper:  %s\n" "$STATUS_FASTER_WHISPER"
echo   "  ---"
printf "  Mic (PyAudio):   %s\n" "$STATUS_MIC"
echo ""
echo "  Venv Python path:"
echo "    $VENV_DIR/bin/python3"
echo ""
echo "  Next steps:"
echo "    1. Set OPENROUTER_API_KEY (or ANTHROPIC_API_KEY) for LLM access"
echo "    2. Run:  wails dev"
echo "    3. The Go app will launch the daemon automatically from:"
echo "       $VENV_DIR/bin/python3"
echo ""

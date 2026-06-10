#!/usr/bin/env bash
# Friday — Jarvis v0.3.0+ mobile companion installer
# Usage: bash install-friday.sh
#
# The pair host (the computer Friday talks to) can be either macOS or
# Windows — both ship the same Echo server on port 4422 and the same
# pairing-QR flow. This script is bash so it's commonly run from macOS
# or WSL; on a vanilla Windows host, just open the website URL below
# manually instead of running this script.

set -euo pipefail

EAS_URL="https://u.expo.dev/4ec82a4b-3506-48da-ba60-114dae1ce9ba?channel=production"
WEBSITE_URL="https://jarvis-workflow-manager.vercel.app/#friday"

cat <<EOF
╔══════════════════════════════════════════════════════════════╗
║  Friday — Jarvis v0.3.0+ mobile companion                    ║
╚══════════════════════════════════════════════════════════════╝

Friday runs inside Expo Go on your phone. No App Store account
required, no developer signing dance.

The pair host (the computer running Jarvis that Friday talks to) can
be a Mac OR a Windows PC — both expose the same Echo server on port
4422 and use the same pairing QR. Pick whichever you have.

INSTALL STEPS:

  1. On your phone, install Expo Go from the App Store / Play Store:
        iOS:     https://apps.apple.com/app/expo-go/id982107779
        Android: https://play.google.com/store/apps/details?id=host.exp.exponent

  2. Open this URL in a browser on your computer:
        ${WEBSITE_URL}

     A QR code will appear. Point your phone's camera at it.
     Expo Go opens automatically and loads Friday.

  3. On the desktop side (Mac OR Windows), open Jarvis →
     Settings → Connections → "Connect Friday phone". Scan THAT QR
     with Friday.

     Done. Voice now relays desktop ↔ phone.

EOF

# Best-effort: open the website page in the default browser
if command -v open >/dev/null 2>&1; then
    echo "Opening ${WEBSITE_URL} in your browser..."
    open "${WEBSITE_URL}"
elif command -v xdg-open >/dev/null 2>&1; then
    xdg-open "${WEBSITE_URL}"
else
    echo "(Open ${WEBSITE_URL} manually in your browser.)"
fi

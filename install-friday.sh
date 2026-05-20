#!/usr/bin/env bash
# Friday — Jarvis v0.3.0 mobile companion installer
# Usage: bash install-friday.sh

set -euo pipefail

EAS_URL="https://u.expo.dev/REPLACE_WITH_EAS_PROJECT_ID?channel=production"
WEBSITE_URL="https://jarvis.namanchopra.dev/friday"

cat <<EOF
╔══════════════════════════════════════════════════════════════╗
║  Friday — Jarvis v0.3.0 mobile companion                     ║
╚══════════════════════════════════════════════════════════════╝

Friday runs inside Expo Go on your phone. No App Store account
required, no developer signing dance.

INSTALL STEPS:

  1. On your phone, install Expo Go from the App Store / Play Store:
        iOS:     https://apps.apple.com/app/expo-go/id982107779
        Android: https://play.google.com/store/apps/details?id=host.exp.exponent

  2. Open this URL in a browser on your computer:
        ${WEBSITE_URL}

     A QR code will appear. Point your phone's camera at it.
     Expo Go opens automatically and loads Friday.

  3. On the Mac side, open Jarvis → Settings → Connections →
     "Connect Friday phone". Scan THAT QR with Friday.

     Done. Voice now relays Mac ↔ phone.

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

#!/bin/bash
# Start Jarvis backend + mobile server
# Run with: ./start-jarvis.sh

cd "$(dirname "$0")"

echo "Starting Jarvis backend..."
wails dev &
WAILS_PID=$!

echo "Starting Expo mobile server..."
cd mobile
npx expo start --tunnel &
EXPO_PID=$!

echo ""
echo "==================================="
echo "  Jarvis is running!"
echo "  Backend:  http://localhost:4422"
echo "  Mobile:   Scan QR in Expo Go"
echo "  Wails:    PID $WAILS_PID"
echo "  Expo:     PID $EXPO_PID"
echo "==================================="
echo ""
echo "Press Ctrl+C to stop everything"

trap "kill $WAILS_PID $EXPO_PID 2>/dev/null; exit" INT TERM
wait

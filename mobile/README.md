# Friday — Jarvis v0.3.0 mobile companion

## First-time EAS setup

1. Sign up at expo.dev, create a project named `jarvis-friday`
2. From `mobile/`, run `eas init` and accept the project ID
3. Replace `REPLACE_WITH_EAS_PROJECT_ID` in `mobile/app.json` with the real ID (3 places)
4. Add repo variable `EAS_PROJECT_ID` (same value)
5. Add repo secret `EXPO_TOKEN` (from expo.dev → Access Tokens)
6. Push to main — the `mobile-update.yml` workflow publishes to the `production` channel

## Local development

```bash
cd mobile
bun install
bun start  # opens Expo Dev Tools; scan QR with Expo Go on your phone
```

## Distribution URL

After the first successful publish, the public URL is `https://u.expo.dev/<project-id>?channel=production`. Render a QR for that URL on the website's `/friday` page (TASK-027).

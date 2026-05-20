# Friday Fonts

This directory holds the font files Friday loads at startup via `expo-font`
in `mobile/app/_layout.tsx`.

## Bundled (already in repo)

- `SpaceMono-Regular.ttf` — original Expo template font; kept so the existing
  templates still resolve. Will be retired once SF Mono is in place.

## User-supplied (NOT committed)

The HUD design tokens (`mobile/lib/hud-tokens.ts`) reference two SF Mono files:

| Filename                  | Used as              | Source                                                                       |
|---------------------------|----------------------|------------------------------------------------------------------------------|
| `SFMono-Regular.otf`      | `fontFamilies.mono`  | Apple SF Mono — https://developer.apple.com/fonts/                           |
| `SFMono-Bold.otf`         | `fontFamilies.monoBold` | Same                                                                       |

These are **not** committed to the repo because Apple's SF Mono license does
not permit redistribution outside Apple platform development contexts.

### How to add them

1. Download Apple's SF Pro / SF Mono bundle from
   https://developer.apple.com/fonts/ (requires a free Apple Developer account).
2. Extract the `SFMono-Regular.otf` and `SFMono-Bold.otf` files.
3. Drop them into this directory (`mobile/assets/fonts/`).
4. In `mobile/lib/hud-tokens.ts`, flip the `SF_MONO_AVAILABLE` constant to
   `true`.
5. In `mobile/app/_layout.tsx`, uncomment the two `require()` lines inside
   the `useFonts({ ... })` call.

### Fallback when they're missing

The fallback chain in `hud-tokens.ts` resolves to platform-native mono:

- **iOS** → `Menlo` (preinstalled on every iOS device since iOS 7)
- **Android** → `monospace` (the Android system mono alias)

So Friday renders correctly in monospace even without the SF Mono OTFs
present — it just won't be the exact-pixel-match SF Mono glyphs the Mac HUD
uses.

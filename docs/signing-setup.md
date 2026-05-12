# macOS code signing and notarization setup

This guide explains how to configure the GitHub Actions secrets that the `release.yml` workflow uses to produce a properly signed and notarized DMG.

## Prerequisites

- An active [Apple Developer Program](https://developer.apple.com/programs/) membership ($99/year)
- Xcode installed locally (for certificate export)
- Access to the repo's Settings → Secrets and variables → Actions

## Step 1: Create a Developer ID Application certificate

1. Open **Keychain Access** on your Mac.
2. Go to **Keychain Access → Certificate Assistant → Request a Certificate From a Certificate Authority**.
3. Fill in your email and name, select "Saved to disk", and save the CSR.
4. Go to [developer.apple.com/account/resources/certificates](https://developer.apple.com/account/resources/certificates).
5. Click **+**, select **Developer ID Application**, upload your CSR, and download the certificate.
6. Double-click the `.cer` file to install it into Keychain Access.

## Step 2: Export the .p12 file

1. In **Keychain Access**, find the certificate under "My Certificates" (it shows as "Developer ID Application: Your Name (TEAM_ID)").
2. Right-click → **Export**. Choose `.p12` format.
3. Set a strong password — this becomes `APPLE_CERTIFICATE_PASSWORD`.

## Step 3: Base64-encode the certificate

```bash
base64 -i Certificates.p12 | pbcopy
```

The clipboard now contains the value for `APPLE_CERTIFICATE`.

## Step 4: Create an app-specific password

Apple's notarization service requires an app-specific password (not your regular Apple ID password):

1. Go to [appleid.apple.com](https://appleid.apple.com) → Sign-In and Security → App-Specific Passwords.
2. Click **Generate** and name it something like "GitHub Actions Notarization".
3. Copy the generated password — this becomes `APPLE_PASSWORD`.

## Step 5: Find your Team ID

Your Team ID is visible at [developer.apple.com/account](https://developer.apple.com/account) under Membership Details. It's a 10-character alphanumeric string (e.g., `ABC123DEF4`).

## Step 6: Determine your signing identity

The signing identity string is the full common name of your certificate. Find it with:

```bash
security find-identity -v -p codesigning
```

Look for the line containing "Developer ID Application". The value you need is the quoted string, e.g.:

```
"Developer ID Application: Naman Chopra (ABC123DEF4)"
```

This entire string (including quotes removed) becomes `APPLE_SIGNING_IDENTITY`.

## Step 7: Configure GitHub secrets

Go to your repo → Settings → Secrets and variables → Actions → New repository secret. Add each:

| Secret | Value |
|--------|-------|
| `APPLE_CERTIFICATE` | Base64-encoded `.p12` file contents (from step 3) |
| `APPLE_CERTIFICATE_PASSWORD` | Password you set when exporting the `.p12` |
| `APPLE_SIGNING_IDENTITY` | `Developer ID Application: Your Name (TEAM_ID)` |
| `APPLE_ID` | Your Apple ID email address |
| `APPLE_PASSWORD` | App-specific password (from step 4) |
| `TEAM_ID` | 10-character Apple Developer Team ID |
| `KEYCHAIN_PASSWORD` | Any random string (used for the temporary CI keychain) |
| `GH_PAT` | GitHub Personal Access Token with `repo` scope |

## Step 8: Create the GH_PAT

The workflow uses `GH_PAT` instead of the default `GITHUB_TOKEN` because the default token can't trigger other workflows or create releases on tags it didn't push.

1. Go to [github.com/settings/tokens](https://github.com/settings/tokens) → Generate new token (classic).
2. Select scope: `repo` (full control of private repositories).
3. Copy the token and save it as the `GH_PAT` secret.

## Verification

Push a tag to trigger the workflow:

```bash
git tag v0.1.3
git push origin v0.1.3
```

The workflow will:
1. Build `Jarvis.app` with Wails
2. Import your certificate into a temporary keychain
3. Sign all nested `.dylib`/`.so` files individually
4. Sign the main `.app` bundle with hardened runtime + timestamp
5. Create the DMG
6. Sign the DMG
7. Submit the DMG to Apple's notarization service (waits for approval)
8. Staple the notarization ticket to the DMG
9. Upload the DMG to a GitHub Release

After notarization succeeds, users can open the app without any Gatekeeper warnings.

## Troubleshooting

**"The signature is invalid"** — The signing identity string doesn't match what's in the certificate. Run `security find-identity -v -p codesigning` on your Mac to get the exact string.

**Notarization fails with "invalid credentials"** — Confirm `APPLE_ID` is your Apple ID email, `APPLE_PASSWORD` is an app-specific password (not your account password), and `TEAM_ID` matches your membership.

**Notarization fails with "hardened runtime not enabled"** — The entitlements.plist must be applied during signing. The workflow handles this, but if you've modified the signing step, ensure `--options runtime` and `--entitlements` are both present.

**"The specified item could not be found in the keychain"** — The `APPLE_CERTIFICATE` base64 encoding may be corrupted. Re-export and re-encode. Ensure no trailing newlines were added.

**"resource fork, Finder info, or similar detritus not allowed"** — Caused by re-signing inner Mach-O binaries with `--deep` after they were already signed. The workflow signs inner `.dylib`/`.so` files first, then re-signs the bundle. If you add `--deep` back to the outer sign, this error returns. Fix: drop `--deep` and ensure the inner find loop catches every Mach-O binary (see "Signing scope" below).

**Notarization rejects with "binary is not signed with a valid Developer ID"** — A Mach-O binary inside `Resources/python/bin/` (e.g. the Python interpreter itself) was missed by the inner find loop, which only matches `*.dylib` and `*.so`. The Python executable is a Mach-O exec with no extension. Either extend the find pattern, or rely on the outer bundle sign without `--deep` after explicitly signing the interpreter.

## Signing scope notes

The workflow signs in two passes:

1. **Inner pass** — `find ... \( -name "*.dylib" -o -name "*.so" \) -exec codesign ...` signs every dynamic library and Python extension module under the bundle.
2. **Outer pass** — signs the `.app` itself, which propagates the seal across `Contents/MacOS/jarvis` and any unsigned executable that the inner pass missed.

If you bundle additional native binaries (Python interpreter, ffmpeg, helper tools), extend the inner pass to cover them. Two common approaches:

```bash
# Explicitly include known bin paths
find build/bin/Jarvis.app \( \
    -name "*.dylib" -o -name "*.so" -o \
    -path "*/Resources/python/bin/*" \) -type f -exec \
    codesign --force --options runtime \
      --entitlements build/darwin/entitlements.plist \
      --sign "$APPLE_SIGNING_IDENTITY" --timestamp {} \;
```

```bash
# Or detect Mach-O by magic bytes (more thorough, slower)
find build/bin/Jarvis.app -type f -perm +111 -exec sh -c '
    file "$1" | grep -q "Mach-O" && codesign --force --options runtime \
      --entitlements build/darwin/entitlements.plist \
      --sign "$APPLE_SIGNING_IDENTITY" --timestamp "$1"
' _ {} \;
```

`--deep` is deprecated by Apple since Xcode 15 — do not add it back. It re-signs already-signed inner items and frequently produces the "resource fork" error on PyTorch/NumPy wheels.

## post-build.sh redundancy

`build/scripts/post-build.sh` ends with an ad-hoc codesign (`--sign -`). The CI workflow re-signs with the Developer ID certificate afterwards, so the ad-hoc seal is overwritten. The ad-hoc step exists so local `wails build` runs (without CI secrets) still produce a runnable bundle for development. It is harmless in CI but wastes a few seconds — skip it via an env guard if you want to optimize:

```bash
if [[ -z "${CI:-}" ]]; then
    codesign --force --options runtime \
      --entitlements "${REPO_ROOT}/build/darwin/entitlements.plist" \
      --sign - "${APP_BUNDLE}"
fi
```

## Why `GH_PAT` instead of `GITHUB_TOKEN`

The default `GITHUB_TOKEN` with `contents: write` permission can create releases. The reason to use a PAT is event propagation: releases (or tags) created by `GITHUB_TOKEN` **do not trigger downstream workflows**. If you have a `release: published` workflow (e.g. publishing to a website, mirroring to another registry) it will silently not fire. Using a PAT makes the release appear as a "real user" action, which does trigger downstream workflows.

If you do not have any downstream automation that listens for `release` events, `GITHUB_TOKEN` is sufficient and avoids the rotation burden of a PAT.

## Modernization: App Store Connect API key

This workflow authenticates `notarytool` with an Apple ID + app-specific password. That works but is now considered legacy. The current best practice is an App Store Connect API key:

1. Visit [appstoreconnect.apple.com/access/api](https://appstoreconnect.apple.com/access/api), create a key with **Developer** access.
2. Download the `.p8` file (one-time download — store it securely).
3. Note the **Key ID** and **Issuer ID**.
4. Base64-encode the `.p8` and store as a secret (e.g. `APPSTORE_API_KEY_P8`).
5. Add `APPSTORE_API_KEY_ID` and `APPSTORE_API_ISSUER_ID` secrets.
6. Replace the notarytool invocation with:

```bash
echo "$APPSTORE_API_KEY_P8" | base64 --decode > "$RUNNER_TEMP/AuthKey.p8"
xcrun notarytool submit "$DMG_PATH" \
    --key "$RUNNER_TEMP/AuthKey.p8" \
    --key-id "$APPSTORE_API_KEY_ID" \
    --issuer "$APPSTORE_API_ISSUER_ID" \
    --wait
```

Benefits over app-specific password: revocable per-key, no MFA risk, no Apple ID password exposure, scoped permissions.

## Keychain search list caveat

The "Import certificate" step runs:

```bash
security list-keychains -d user -s "$KEYCHAIN_PATH" login.keychain
```

This **replaces** the user's keychain search list with just the temp keychain + `login.keychain`, dropping `System.keychain`. On the ephemeral GitHub runner this is harmless because the keychain is destroyed at job end, but if you ever adapt this workflow to run on a long-lived self-hosted runner, prefer appending to preserve the existing search list:

```bash
security list-keychain -d user -s "$KEYCHAIN_PATH" $(security list-keychain -d user | tr -d '"')
```

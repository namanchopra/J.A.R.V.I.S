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

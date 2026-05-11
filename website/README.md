# Jarvis marketing site

Next.js 15 + Tailwind 4 + react-three-fiber landing page for J.A.R.V.I.S.

## Run

```
cd website
npm install
npm run dev          # → http://localhost:3000
npm run build && npm run start
```

## Deploy

The site is a standard Next.js App Router app. Two easy deploy paths:

**Vercel (recommended — handles client-side three.js correctly):**
1. Push to GitHub (already done if you're reading this in the repo).
2. Import the repo at <https://vercel.com/new>.
3. Set **Root Directory** to `website`. Framework auto-detects as Next.js.
4. Deploy. Done.

**GitHub Pages (static export):**
Static export works since this site has no server actions. Add to `next.config.ts`:

```ts
const config: NextConfig = { output: 'export', images: { unoptimized: true } }
```

Then `npm run build` produces `out/` which you can push to a `gh-pages` branch with `actions/upload-pages-artifact`.

## Files

- `app/page.tsx` — Hero + Demo + Features + Install + Footer
- `app/layout.tsx` — Root metadata + global CSS injection
- `app/globals.css` — Jarvis design palette (cyan/dark, JetBrains Mono, scanlines)
- `components/JarvisOrb3D.tsx` — react-three-fiber orb with rings + particle field
- `components/DemoTranscript.tsx` — looping "Hey Jarvis" mock transcript
- `components/StarButton.tsx` — Live GitHub stargazer count via public API

## Hosting the heavy DMG

The site links to `github.com/namanchopra/J.A.R.V.I.S/releases/latest` for the standard ~356 MB DMG. If you want to offer a "fat DMG" with model weights pre-baked (~2.4 GB), GitHub release assets cap at 2 GiB so you need an external host. Options:

- **Cloudflare R2** — free egress, S3-compatible. Point the "Download" button at a public R2 URL.
- **Hugging Face Spaces** — can host arbitrary file sizes; works well alongside the model weights themselves.
- **A self-hosted CDN** — Bunny.net, Fastly, etc.

Whichever you pick, swap the `DMG_URL` constant near the top of `app/page.tsx`.

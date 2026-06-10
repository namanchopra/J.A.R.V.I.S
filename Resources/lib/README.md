# Resources/lib/

Runtime bundle staging directory for native shared libraries shipped alongside
the Wails-built `jarvis.exe` on Windows. Populated at build time by
`build\scripts\post-build.ps1` (TASK-035 of `plans/jarvis-windows-port.md`).

## Contents at install time

| File | Source | Owner |
|------|--------|-------|
| `portaudio.dll` | `build\portaudio\<arch>\portaudio.dll` (TASK-007) | `build\scripts\fetch-portaudio.ps1` |

## Why this directory exists in source

The DLLs themselves are binary artefacts and live OUTSIDE source control —
they are fetched + SHA-verified at build time and staged here by the
post-build step. This `README.md` keeps the empty `Resources\lib\` directory
present so:

1. `install-daemon.ps1`'s `Resolve-PortaudioDll` helper has a well-known
   search root that is documented in source.
2. Developers building locally see the expected layout without first running
   the full `post-build.ps1`.
3. Git tracks the directory's existence (without it, `git clone` would not
   recreate the path).

If you need the actual `portaudio.dll`, run:

```powershell
pwsh build\scripts\fetch-portaudio.ps1 -Arch x64
```

then copy the result from `build\portaudio\x64\portaudio.dll` into this
directory (or let `post-build.ps1` do it).

## macOS equivalent

On macOS the same role is played by `Contents/Frameworks/libportaudio.2.dylib`
inside the `.app` bundle — staged by `build/scripts/post-build.sh`. See
`scripts/setup/install-daemon.sh::ensure_portaudio` for the macOS-side
preflight check that mirrors `Resolve-PortaudioDll` in
`scripts/setup/install-daemon.ps1`.

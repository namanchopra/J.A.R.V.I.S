// ---------------------------------------------------------------------------
// Theme -- dark/light mode toggle utility
// ---------------------------------------------------------------------------

const STORAGE_KEY = 'vd-theme'

export type Theme = 'light' | 'dark'

/** Read persisted theme (defaults to dark). */
export function getTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark') return stored
  } catch {
    // localStorage may be unavailable
  }
  return 'dark'
}

/** Persist theme and apply the `.dark` class on <html>. */
export function setTheme(theme: Theme): void {
  try {
    localStorage.setItem(STORAGE_KEY, theme)
  } catch {
    // localStorage may be unavailable
  }

  if (theme === 'dark') {
    document.documentElement.classList.add('dark')
  } else {
    document.documentElement.classList.remove('dark')
  }
}

/** Toggle between light and dark, returning the new theme. */
export function toggleTheme(): Theme {
  const next: Theme = getTheme() === 'dark' ? 'light' : 'dark'
  setTheme(next)
  return next
}

/** Call once before React render to avoid a flash of wrong theme. */
export function initTheme(): void {
  setTheme(getTheme())
}

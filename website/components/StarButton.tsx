'use client'

import { useEffect, useState } from 'react'

const REPO_API = 'https://api.github.com/repos/namanchopra/J.A.R.V.I.S'
const REPO_URL = 'https://github.com/namanchopra/J.A.R.V.I.S'

export default function StarButton() {
  const [stars, setStars] = useState<number | null>(null)

  useEffect(() => {
    let cancelled = false
    fetch(REPO_API, { headers: { Accept: 'application/vnd.github+json' } })
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (!cancelled && data?.stargazers_count != null) {
          setStars(data.stargazers_count)
        }
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  return (
    <a
      href={REPO_URL}
      target="_blank"
      rel="noreferrer noopener"
      className="jarvis-btn-secondary group"
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="group-hover:fill-jarvis-cyan transition-colors"
        aria-hidden="true"
      >
        <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
      </svg>
      <span>Star</span>
      <span className="text-jarvis-cyan/50">
        {stars == null ? '…' : stars}
      </span>
    </a>
  )
}

// ---------------------------------------------------------------------------
// StatCard -- single dashboard stat card with solid surface
// ---------------------------------------------------------------------------

import { useEffect, useRef } from 'react'
import { motion, useAnimationControls } from 'framer-motion'

interface StatCardProps {
  label: string
  value: number
  colorClass: string      // e.g. "text-green-400"
  bgClass: string         // e.g. "bg-green-500/10"
  borderClass?: string    // e.g. "border-green-500/20"
  accentColor?: string    // e.g. "border-t-green-500" for top accent
  pulse?: boolean
}

export function StatCard({
  label,
  value,
  colorClass,
  bgClass: _bgClass,
  borderClass: _borderClass = 'border-border',
  accentColor = 'border-t-blue-500',
  pulse: _pulse = false,
}: StatCardProps): React.ReactElement {
  const controls = useAnimationControls()
  const prevValueRef = useRef(value)

  // Animate number on value change
  useEffect(() => {
    if (prevValueRef.current !== value) {
      prevValueRef.current = value
      void controls.start({
        scale: [1, 1.05, 1],
        transition: { duration: 0.3 },
      })
    }
  }, [value, controls])

  return (
    <div
      className={`rounded-xl border border-border bg-surface border-t-2 ${accentColor} px-4 py-3`}
    >
      <p className="text-xs font-medium text-secondary uppercase tracking-wider">
        {label}
      </p>
      <motion.p
        animate={controls}
        className={`text-3xl font-bold mt-1 ${colorClass}`}
      >
        {value}
      </motion.p>
    </div>
  )
}

import '../../lib/hud-theme'

// ---------------------------------------------------------------------------
// Types (local -- no wailsjs model import)
// ---------------------------------------------------------------------------

interface TotalSpend {
  allTime: number
  thisMonth: number
  today: number
}

interface DailyCost {
  date: string
  inputTokens: number
  outputTokens: number
  costUsd: number
  sessionCount: number
}

interface HudCostPanelProps {
  spend: TotalSpend | null
  daily: DailyCost[]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatUsd(value: number): string {
  return `$${value.toFixed(2)}`
}

/** Build SVG polyline points from the last 7 days of cost data. */
function buildSparklinePoints(
  daily: DailyCost[],
  width: number,
  height: number,
  padding: number,
): string {
  const last7 = daily.slice(-7)

  // Need at least 2 points for a meaningful line
  if (last7.length < 2) {
    const midY = height / 2
    return `0,${midY} ${width},${midY}`
  }

  const costs = last7.map((d) => d.costUsd)
  const maxCost = Math.max(...costs)
  const minCost = Math.min(...costs)
  const range = maxCost - minCost

  const drawHeight = height - padding * 2

  return last7
    .map((d, i) => {
      const x = (i / (last7.length - 1)) * width
      // Normalize: 0 = bottom, max = top. If all values equal, draw flat at mid.
      const normalizedY =
        range === 0 ? 0.5 : (d.costUsd - minCost) / range
      const y = padding + drawHeight * (1 - normalizedY)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

const SPARKLINE_WIDTH = 200
const SPARKLINE_HEIGHT = 30
const SPARKLINE_PADDING = 3

const COST_ROWS: { label: string; key: keyof TotalSpend }[] = [
  { label: 'TODAY', key: 'today' },
  { label: 'MONTH', key: 'thisMonth' },
  { label: 'ALL TIME', key: 'allTime' },
]

export function HudCostPanel({ spend, daily }: HudCostPanelProps): React.ReactElement {
  const points = buildSparklinePoints(
    daily,
    SPARKLINE_WIDTH,
    SPARKLINE_HEIGHT,
    SPARKLINE_PADDING,
  )

  return (
    <div className="hud-panel flex flex-col gap-2">
      <span className="hud-label">COSTS</span>

      {/* Readout rows */}
      <div className="space-y-1">
        {COST_ROWS.map(({ label, key }) => (
          <div key={key} className="flex justify-between text-xs">
            <span className="hud-text-dim">{label}</span>
            <span className="hud-value">
              {formatUsd(spend?.[key] ?? 0)}
            </span>
          </div>
        ))}
      </div>

      {/* Sparkline -- last 7 days */}
      <svg
        width="100%"
        height={SPARKLINE_HEIGHT}
        viewBox={`0 0 ${SPARKLINE_WIDTH} ${SPARKLINE_HEIGHT}`}
        preserveAspectRatio="none"
        aria-label="Cost sparkline for the last 7 days"
        role="img"
      >
        <polyline
          points={points}
          fill="none"
          stroke="#00ffcc"
          strokeWidth="1.5"
          strokeLinejoin="round"
          strokeLinecap="round"
        />
      </svg>
    </div>
  )
}

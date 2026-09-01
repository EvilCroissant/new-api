/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import type { PerformanceGroup } from '@/features/performance-metrics/types'

import type { LatencyTimePoint } from './mock-stats'

export function toLatencySeries(
  groups: PerformanceGroup[]
): LatencyTimePoint[] {
  const points: Array<{ ts: number; point: LatencyTimePoint }> = []

  for (const group of groups) {
    for (const point of group.series) {
      if (point.avg_ttft_ms <= 0) continue
      points.push({
        ts: point.ts,
        point: {
          timestamp: new Date(point.ts * 1000).toISOString(),
          group: group.group,
          ttft_ms: point.avg_ttft_ms,
        },
      })
    }
  }

  return points
    .sort(
      (a, b) => a.ts - b.ts || a.point.group.localeCompare(b.point.group)
    )
    .map(({ point }) => point)
}

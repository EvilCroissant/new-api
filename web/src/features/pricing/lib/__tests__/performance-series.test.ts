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

import { describe, expect, test } from 'vitest'

import type { PerformanceGroup } from '@/features/performance-metrics/types'

import { toLatencySeries } from '../performance-series'

function group(
  name: string,
  series: PerformanceGroup['series']
): PerformanceGroup {
  return {
    group: name,
    avg_ttft_ms: 0,
    avg_latency_ms: 0,
    success_rate: 100,
    avg_tps: 0,
    series,
  }
}

describe('toLatencySeries', () => {
  test('keeps one latency point per group and timestamp', () => {
    const result = toLatencySeries([
      group('GPT Pro', [
        {
          ts: 1720003600,
          avg_ttft_ms: 320,
          avg_latency_ms: 0,
          success_rate: 100,
          avg_tps: 0,
        },
      ]),
      group('GPT Plus', [
        {
          ts: 1720003600,
          avg_ttft_ms: 180,
          avg_latency_ms: 0,
          success_rate: 100,
          avg_tps: 0,
        },
      ]),
    ])

    expect(result).toHaveLength(2)
    expect(result.map(({ group: name, ttft_ms }) => [name, ttft_ms])).toEqual([
      ['GPT Plus', 180],
      ['GPT Pro', 320],
    ])
  })

  test('sorts points by time and omits buckets without TTFT', () => {
    const result = toLatencySeries([
      group('GPT Plus', [
        {
          ts: 1720007200,
          avg_ttft_ms: 0,
          avg_latency_ms: 0,
          success_rate: 100,
          avg_tps: 0,
        },
        {
          ts: 1720000000,
          avg_ttft_ms: 240,
          avg_latency_ms: 0,
          success_rate: 100,
          avg_tps: 0,
        },
      ]),
    ])

    expect(result.map(({ ttft_ms }) => ttft_ms)).toEqual([240])
    expect(result[0]?.timestamp).toBe(
      new Date(1720000000 * 1000).toISOString()
    )
  })
})

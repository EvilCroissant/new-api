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

export type GroupSnapshotItem = {
  id: string
  name: string
  description: string
  multiplier: number | null
  multiplierLabel: string | null
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function toNumber(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : null
  }
  return null
}

function getGroupSnapshotItem(
  group: unknown,
  rates: Record<string, unknown>,
  fallbackID?: string
): GroupSnapshotItem | null {
  if (!isRecord(group)) return null
  const id = group.id ?? fallbackID
  if (typeof id !== 'number' && typeof id !== 'string') return null

  const groupID = String(id)
  const name =
    typeof group.name === 'string' && group.name.trim() !== ''
      ? group.name
      : groupID
  const descriptionValue = group.description ?? group.desc
  const description =
    typeof descriptionValue === 'string' ? descriptionValue.trim() : ''
  const rawMultiplier =
    group.multiplier ?? rates[groupID] ?? group.rate_multiplier ?? group.ratio

  return {
    id: groupID,
    name,
    description,
    multiplier: toNumber(rawMultiplier),
    multiplierLabel:
      typeof rawMultiplier === 'string' && toNumber(rawMultiplier) === null
        ? rawMultiplier
        : null,
  }
}

export function getGroupSnapshotItems(snapshot: unknown): GroupSnapshotItem[] {
  if (!isRecord(snapshot)) return []
  const rates = isRecord(snapshot.rates) ? snapshot.rates : {}

  if (Array.isArray(snapshot.groups)) {
    return snapshot.groups.flatMap((group) => {
      const result = getGroupSnapshotItem(group, rates)
      return result ? [result] : []
    })
  }
  if (!isRecord(snapshot.data)) return []

  return Object.entries(snapshot.data).flatMap(([groupID, group]) => {
    const result = getGroupSnapshotItem(group, rates, groupID)
    return result ? [result] : []
  })
}

export function formatGroupMultiplier(group: GroupSnapshotItem): string {
  if (group.multiplier !== null) return `${group.multiplier}x`
  return group.multiplierLabel || '-'
}

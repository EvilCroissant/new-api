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

import { useQuery } from '@tanstack/react-query'
import { type ReactNode, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { getUpstreamMonitor } from '../api'

type UpstreamMonitorDetailDialogProps = {
  monitorId: number | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

type GroupMultiplier = {
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

function getGroupMultiplier(
  group: unknown,
  rates: Record<string, unknown>,
  fallbackID?: string
): GroupMultiplier | null {
  if (!isRecord(group)) return null
  const id = group.id ?? fallbackID
  if (typeof id !== 'number' && typeof id !== 'string') return null
  const groupID = String(id)
  const name =
    typeof group.name === 'string' && group.name.trim() !== ''
      ? group.name
      : groupID
  const rawMultiplier =
    group.multiplier ?? rates[groupID] ?? group.rate_multiplier ?? group.ratio
  let description = ''
  if (typeof group.description === 'string') {
    description = group.description
  } else if (typeof group.desc === 'string') {
    description = group.desc
  }
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

function getGroupMultipliers(snapshot: unknown): GroupMultiplier[] {
  if (!isRecord(snapshot)) return []
  const rates = isRecord(snapshot.rates) ? snapshot.rates : {}
  if (Array.isArray(snapshot.groups)) {
    return snapshot.groups.flatMap((group) => {
      const result = getGroupMultiplier(group, rates)
      return result ? [result] : []
    })
  }
  if (!isRecord(snapshot.data)) return []

  return Object.entries(snapshot.data).flatMap(([groupID, group]) => {
    const result = getGroupMultiplier(group, rates, groupID)
    return result ? [result] : []
  })
}

function formatMultiplier(group: GroupMultiplier): string {
  if (group.multiplier !== null) return `${group.multiplier}x`
  return group.multiplierLabel || '-'
}

export function UpstreamMonitorDetailDialog(
  props: UpstreamMonitorDetailDialogProps
) {
  const { t } = useTranslation()
  const detailQuery = useQuery({
    queryKey: ['upstream-monitor', props.monitorId],
    queryFn: () =>
      props.monitorId ? getUpstreamMonitor(props.monitorId) : null,
    enabled: props.open && props.monitorId !== null,
    retry: false,
  })
  const monitor = detailQuery.data?.data
  const groupMultipliers = useMemo(
    () => getGroupMultipliers(monitor?.groups),
    [monitor?.groups]
  )
  let detailContent: ReactNode
  if (detailQuery.isLoading) {
    detailContent = (
      <div className='flex justify-center py-10'>
        <Spinner aria-label={t('Loading')} />
      </div>
    )
  } else if (detailQuery.isError || !detailQuery.data?.success) {
    detailContent = (
      <p className='text-destructive py-8 text-center text-sm'>
        {detailQuery.data?.message ||
          t('Could not load upstream monitor details')}
      </p>
    )
  } else {
    detailContent = (
      <section>
        <h3 className='mb-2 text-sm font-medium'>{t('Groups')}</h3>
        {groupMultipliers.length > 0 ? (
          <div className='max-h-72 overflow-auto rounded-lg border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Group')}</TableHead>
                  <TableHead>{t('Description')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Multiplier')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {groupMultipliers.map((group) => (
                  <TableRow key={group.id}>
                    <TableCell className='font-medium'>{group.name}</TableCell>
                    <TableCell className='text-muted-foreground'>
                      {group.description || '-'}
                    </TableCell>
                    <TableCell className='text-right font-medium tabular-nums'>
                      {formatMultiplier(group)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('No group snapshot available.')}
          </p>
        )}
      </section>
    )
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='max-h-[calc(100dvh-2rem)] overflow-hidden sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('Upstream monitor details')}</DialogTitle>
          <DialogDescription className='truncate font-mono text-xs'>
            {monitor?.base_url || ''}
          </DialogDescription>
        </DialogHeader>

        <div className='flex max-h-[60vh] flex-col gap-4 overflow-y-auto pr-1'>
          {detailContent}
        </div>

        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

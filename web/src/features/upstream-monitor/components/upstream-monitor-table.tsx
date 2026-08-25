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

import { Database, RefreshCw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatCurrencyFromUSD } from '@/lib/currency'
import { formatNumber, formatTimestampRelative } from '@/lib/format'

import type { UpstreamMonitor } from '../types'

type UpstreamMonitorTableProps = {
  monitors: UpstreamMonitor[]
  isRoot: boolean
  syncingId?: number
  deletingId?: number
  onSync: (id: number) => void
  onDelete: (id: number) => void
  onViewDetails: (id: number) => void
}

function providerLabel(provider: UpstreamMonitor['provider']): string {
  return provider === 'newapi' ? 'New API' : 'Sub2API'
}

export function UpstreamMonitorTable(props: UpstreamMonitorTableProps) {
  const { t } = useTranslation()
  const [deleteCandidate, setDeleteCandidate] =
    useState<UpstreamMonitor | null>(null)

  if (!props.monitors.length) {
    return (
      <EmptyState
        icon={Database}
        title={t('No upstream monitors')}
        description={t(
          'Add an upstream monitor to view account balance, groups, and pricing.'
        )}
      />
    )
  }

  return (
    <>
      <div className='overflow-x-auto rounded-xl border'>
        <Table className='min-w-[860px]'>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Site')}</TableHead>
              <TableHead className='text-right'>
                {t('Available balance')}
              </TableHead>
              <TableHead className='text-right'>{t('Groups')}</TableHead>
              <TableHead className='text-right'>
                {t('Pricing entries')}
              </TableHead>
              <TableHead>{t('Last synchronized')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead className='w-[180px] text-right'>
                {t('Actions')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.monitors.map((monitor) => {
              const isSyncing = props.syncingId === monitor.id
              const isDeleting = props.deletingId === monitor.id
              let syncStatus = (
                <span className='text-muted-foreground text-sm'>-</span>
              )
              if (monitor.last_synced_at > 0) {
                syncStatus = (
                  <Badge variant='secondary' className='font-normal'>
                    {t('Synchronized')}
                  </Badge>
                )
              }
              if (monitor.last_error) {
                syncStatus = (
                  <TooltipProvider>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Badge
                            variant='destructive'
                            className='cursor-help font-normal'
                          />
                        }
                      >
                        {t('Sync failed')}
                      </TooltipTrigger>
                      <TooltipContent className='max-w-sm break-words'>
                        {monitor.last_error}
                      </TooltipContent>
                    </Tooltip>
                  </TooltipProvider>
                )
              }
              return (
                <TableRow key={monitor.id}>
                  <TableCell className='max-w-[260px]'>
                    <div className='flex min-w-0 flex-col gap-1'>
                      <span className='truncate font-medium'>
                        {monitor.name}
                      </span>
                      <div className='flex min-w-0 items-center gap-1.5'>
                        <Badge
                          variant='secondary'
                          className='shrink-0 font-normal'
                        >
                          {providerLabel(monitor.provider)}
                        </Badge>
                        <span className='text-muted-foreground truncate font-mono text-xs'>
                          {monitor.base_url}
                        </span>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className='text-right font-medium tabular-nums'>
                    {monitor.balance_available
                      ? formatCurrencyFromUSD(monitor.balance_usd)
                      : '-'}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {formatNumber(monitor.group_count)}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {formatNumber(monitor.pricing_count)}
                  </TableCell>
                  <TableCell className='text-muted-foreground whitespace-nowrap'>
                    {monitor.last_synced_at > 0
                      ? formatTimestampRelative(monitor.last_synced_at)
                      : t('Never synchronized')}
                  </TableCell>
                  <TableCell>{syncStatus}</TableCell>
                  <TableCell>
                    <div className='flex justify-end gap-1.5'>
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        onClick={() => props.onViewDetails(monitor.id)}
                      >
                        <Database
                          data-icon='inline-start'
                          className='size-3.5'
                        />
                        {t('View data')}
                      </Button>
                      {props.isRoot && (
                        <>
                          <Button
                            type='button'
                            variant='outline'
                            size='icon-sm'
                            aria-label={t('Sync now')}
                            title={t('Sync now')}
                            disabled={isSyncing || isDeleting}
                            onClick={() => props.onSync(monitor.id)}
                          >
                            {isSyncing ? (
                              <Spinner aria-label={t('Loading')} />
                            ) : (
                              <RefreshCw
                                className='size-3.5'
                                aria-hidden='true'
                              />
                            )}
                          </Button>
                          <Button
                            type='button'
                            variant='outline'
                            size='icon-sm'
                            aria-label={t('Delete monitor')}
                            title={t('Delete monitor')}
                            disabled={isSyncing || isDeleting}
                            onClick={() => setDeleteCandidate(monitor)}
                          >
                            {isDeleting ? (
                              <Spinner aria-label={t('Loading')} />
                            ) : (
                              <Trash2 className='size-3.5' aria-hidden='true' />
                            )}
                          </Button>
                        </>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      <AlertDialog
        open={deleteCandidate !== null}
        onOpenChange={(open) => !open && setDeleteCandidate(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete this monitor?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'This removes the saved credential and all monitor snapshots.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (!deleteCandidate) return
                props.onDelete(deleteCandidate.id)
                setDeleteCandidate(null)
              }}
            >
              {t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

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

import {
  Alert02Icon,
  Delete02Icon,
  Edit02Icon,
  LinkSquare01Icon,
  MoreHorizontalIcon,
  RefreshIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Fragment, useState } from 'react'
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
import {
  formatBillingCurrencyFromUSD,
  formatCurrencyFromUSD,
} from '@/lib/currency'
import { formatTimestampRelative } from '@/lib/format'

import type { UpstreamMonitor, UpstreamMonitorUpdateInput } from '../types'
import { UpstreamMonitorCredentialPanel } from './upstream-monitor-credential-panel'

type UpstreamMonitorTableProps = {
  embedded?: boolean
  monitors: UpstreamMonitor[]
  isRoot: boolean
  syncingId?: number
  deletingId?: number
  updatingId?: number
  onSync: (id: number) => void
  onDelete: (id: number) => void
  onUpdateCredentials: (
    id: number,
    input: UpstreamMonitorUpdateInput
  ) => Promise<void>
}

const LOW_BALANCE_USD = 5

type ExpandedPanel = {
  id: number
  kind: 'credentials'
}

function providerLabel(provider: UpstreamMonitor['provider']): string {
  return provider === 'newapi' ? 'New API' : 'Sub2API'
}

export function UpstreamMonitorTable(props: UpstreamMonitorTableProps) {
  const { t } = useTranslation()
  const [deleteCandidate, setDeleteCandidate] =
    useState<UpstreamMonitor | null>(null)
  const [expandedPanel, setExpandedPanel] = useState<ExpandedPanel | null>(null)

  if (!props.monitors.length) {
    if (props.embedded) return null
    return <EmptyState title={t('No upstream monitors')} />
  }

  const content = (
    <>
      {props.monitors.map((monitor) => {
        const isSyncing = props.syncingId === monitor.id
        const isDeleting = props.deletingId === monitor.id
        const isUpdating = props.updatingId === monitor.id
        const isCredentialsExpanded =
          expandedPanel?.id === monitor.id &&
          expandedPanel.kind === 'credentials'
        const isExpanded = isCredentialsExpanded
        const panelID = `upstream-monitor-panel-${monitor.id}`
        const isLowBalance =
          monitor.balance_available && monitor.balance_usd < LOW_BALANCE_USD
        let syncStatus = (
          <span className='text-muted-foreground text-sm'>-</span>
        )
        if (isSyncing) {
          syncStatus = (
            <Badge variant='secondary' className='font-normal'>
              <Spinner aria-hidden='true' />
              {t('Synchronizing')}
            </Badge>
          )
        } else if (monitor.last_error) {
          syncStatus = (
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
          )
        } else if (monitor.last_synced_at > 0) {
          syncStatus = (
            <Badge
              variant='outline'
              className='border-success/40 bg-success/10 text-success font-normal'
            >
              <span
                className='bg-success size-1.5 rounded-full'
                aria-hidden='true'
              />
              {t('Synchronized')}
            </Badge>
          )
        }

        return (
          <Fragment key={monitor.id}>
            <TableRow>
              <TableCell
                className={props.embedded ? 'px-4 py-3' : 'max-w-[280px]'}
              >
                <div className='flex min-w-0 flex-col gap-1'>
                  <div className='flex min-w-0 flex-wrap items-center gap-1.5'>
                    {props.embedded && props.isRoot ? (
                      <Button
                        type='button'
                        variant='link'
                        className='text-foreground/90 hover:text-primary h-auto min-w-0 shrink justify-start p-0 text-left text-sm font-semibold'
                        aria-label={t('Settings')}
                        aria-expanded={isExpanded}
                        aria-controls={panelID}
                        onClick={() =>
                          setExpandedPanel(
                            isExpanded
                              ? null
                              : { id: monitor.id, kind: 'credentials' }
                          )
                        }
                      >
                        <span className='truncate'>{monitor.name}</span>
                      </Button>
                    ) : (
                      <span className='truncate font-medium'>
                        {monitor.name}
                      </span>
                    )}
                    {props.embedded && (
                      <span
                        aria-label={t('Available balance')}
                        className={
                          isLowBalance || monitor.last_error
                            ? 'text-warning text-xs tabular-nums'
                            : 'text-muted-foreground text-xs tabular-nums'
                        }
                      >
                        {monitor.balance_available
                          ? formatBillingCurrencyFromUSD(monitor.balance_usd, {
                              digitsSmall: 6,
                              digitsLarge: 2,
                            })
                          : '-'}
                      </span>
                    )}
                  </div>
                  <div className='flex min-w-0 items-center gap-1.5'>
                    <Badge variant='secondary' className='shrink-0 font-normal'>
                      {providerLabel(monitor.provider)}
                    </Badge>
                    <a
                      href={monitor.base_url}
                      target='_blank'
                      rel='noreferrer'
                      aria-label={`${t('Open upstream site')}: ${monitor.name}`}
                      className='text-muted-foreground hover:text-foreground group/url flex min-w-0 items-center gap-1 font-mono text-xs transition-colors'
                    >
                      <span className='truncate'>{monitor.base_url}</span>
                      <HugeiconsIcon
                        icon={LinkSquare01Icon}
                        strokeWidth={2}
                        className='size-3 shrink-0 opacity-60 transition-opacity group-hover/url:opacity-100'
                        aria-hidden='true'
                      />
                    </a>
                  </div>
                </div>
              </TableCell>
              {props.embedded ? (
                <>
                  <TableCell className='text-muted-foreground text-center'>
                    -
                  </TableCell>
                  <TableCell className='text-muted-foreground text-center'>
                    -
                  </TableCell>
                  <TableCell className='text-muted-foreground text-center'>
                    -
                  </TableCell>
                </>
              ) : (
                <>
                  <TableCell className='text-right font-medium tabular-nums'>
                    <div className='flex items-center justify-end gap-1'>
                      <span className={isLowBalance ? 'text-warning' : ''}>
                        {monitor.balance_available
                          ? formatCurrencyFromUSD(monitor.balance_usd)
                          : '-'}
                      </span>
                      {isLowBalance && (
                        <Tooltip>
                          <TooltipTrigger
                            render={
                              <span
                                tabIndex={0}
                                aria-label={t('Low balance')}
                                className='text-warning inline-flex cursor-help'
                              />
                            }
                          >
                            <HugeiconsIcon
                              icon={Alert02Icon}
                              strokeWidth={2}
                              className='size-3.5'
                              aria-hidden='true'
                            />
                          </TooltipTrigger>
                          <TooltipContent>{t('Low balance')}</TooltipContent>
                        </Tooltip>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className='text-muted-foreground whitespace-nowrap'>
                    {monitor.last_synced_at > 0
                      ? formatTimestampRelative(monitor.last_synced_at)
                      : t('Never synchronized')}
                  </TableCell>
                  <TableCell>{syncStatus}</TableCell>
                </>
              )}
              <TableCell>
                {props.embedded && (
                  <div className='flex flex-col items-center gap-1'>
                    {syncStatus}
                    <span className='text-muted-foreground text-[11px]'>
                      {monitor.last_synced_at > 0
                        ? formatTimestampRelative(monitor.last_synced_at)
                        : t('Never synchronized')}
                    </span>
                  </div>
                )}
                <div
                  className={
                    props.embedded
                      ? 'flex justify-center gap-0.5'
                      : 'flex justify-end gap-0.5'
                  }
                >
                  {props.isRoot && (
                    <>
                      <Tooltip>
                        <TooltipTrigger
                          render={
                            <Button
                              type='button'
                              variant='ghost'
                              size='icon-sm'
                              disabled={isSyncing || isDeleting || isUpdating}
                              aria-label={t('Sync now')}
                              onClick={() => props.onSync(monitor.id)}
                            />
                          }
                        >
                          {isSyncing ? (
                            <Spinner aria-hidden='true' />
                          ) : (
                            <HugeiconsIcon
                              icon={RefreshIcon}
                              strokeWidth={2}
                              aria-hidden='true'
                            />
                          )}
                        </TooltipTrigger>
                        <TooltipContent>{t('Sync now')}</TooltipContent>
                      </Tooltip>

                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              type='button'
                              variant='ghost'
                              size='icon-sm'
                              aria-label={t('More')}
                            />
                          }
                        >
                          <HugeiconsIcon
                            icon={MoreHorizontalIcon}
                            strokeWidth={2}
                            aria-hidden='true'
                          />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align='end' className='w-40'>
                          <DropdownMenuGroup>
                            <DropdownMenuItem
                              disabled={isSyncing || isDeleting || isUpdating}
                              onClick={() =>
                                setExpandedPanel({
                                  id: monitor.id,
                                  kind: 'credentials',
                                })
                              }
                            >
                              <HugeiconsIcon
                                icon={Edit02Icon}
                                strokeWidth={2}
                                aria-hidden='true'
                              />
                              {t('Edit credentials')}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              disabled={isSyncing || isDeleting || isUpdating}
                              variant='destructive'
                              onClick={() => setDeleteCandidate(monitor)}
                            >
                              {isDeleting ? (
                                <Spinner aria-hidden='true' />
                              ) : (
                                <HugeiconsIcon
                                  icon={Delete02Icon}
                                  strokeWidth={2}
                                  aria-hidden='true'
                                />
                              )}
                              {t('Delete monitor')}
                            </DropdownMenuItem>
                          </DropdownMenuGroup>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </>
                  )}
                </div>
              </TableCell>
            </TableRow>
            {isExpanded && (
              <TableRow className='h-auto border-b hover:bg-transparent'>
                <TableCell colSpan={5} className='p-0 whitespace-normal'>
                  {isCredentialsExpanded && (
                    <UpstreamMonitorCredentialPanel
                      monitor={monitor}
                      id={panelID}
                      isSaving={isUpdating}
                      onCancel={() => setExpandedPanel(null)}
                      onSave={(input) =>
                        props.onUpdateCredentials(monitor.id, input)
                      }
                    />
                  )}
                </TableCell>
              </TableRow>
            )}
          </Fragment>
        )
      })}

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

  if (props.embedded) return content

  return (
    <TooltipProvider>
      <div className='overflow-x-auto rounded-xl border'>
        <Table className='min-w-[760px]'>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Site')}</TableHead>
              <TableHead className='text-right'>
                {t('Available balance')}
              </TableHead>
              <TableHead>{t('Last synchronized')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead className='w-[132px] text-right'>
                {t('Actions')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>{content}</TableBody>
        </Table>
      </div>
    </TooltipProvider>
  )
}

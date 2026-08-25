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

import { ArrowRight01Icon, RefreshIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { CircleDollarSign } from 'lucide-react'
import { Fragment, useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { cn } from '@/lib/utils'

import type {
  ChannelProfitConfigInput,
  ChannelProfitProvider,
  ChannelProfitRow,
  ChannelProfitStatus,
} from '../types'
import { ProfitSettingsDialog } from './profit-settings-dialog'

const STATUS_VARIANT: Record<
  ChannelProfitStatus,
  'outline' | 'secondary' | 'warning' | 'destructive'
> = {
  disabled: 'outline',
  pending: 'secondary',
  synced: 'secondary',
  partial: 'warning',
  error: 'destructive',
}

const PROVIDER_LABELS: Partial<Record<ChannelProfitProvider, string>> = {
  new_api: 'New API',
  sub2api: 'Sub2API',
  mixed: 'New API / Sub2API',
}

type ProfitTableProps = {
  rows: ChannelProfitRow[]
  isRoot: boolean
  syncingChannelId?: number
  savingChannelId?: number
  onSync: (channelId: number) => void
  onSaveSettings: (
    channelId: number,
    input: ChannelProfitConfigInput
  ) => Promise<void>
}

function ratioText(ratio: number) {
  return `${ratio.toFixed(4).replace(/0+$/, '').replace(/\.$/, '')}x`
}

function providerLabel(provider: ChannelProfitProvider) {
  return PROVIDER_LABELS[provider] || ''
}

function ProfitSubRows({ row }: { row: ChannelProfitRow }) {
  const { t } = useTranslation()

  return (
    <div
      data-profit-group-details={row.group_id}
      className='bg-muted/10 border-border/30 divide-border/20 w-full divide-y border-y'
    >
      {row.keys.map((key) => {
        const downstreamRatioSummary = [
          ...new Set(key.downstream_rates.map((rate) => ratioText(rate.ratio))),
        ].join(' / ')
        let statusContent = (
          <span
            className='bg-success size-1.5 rounded-full'
            title={t('Synchronized')}
            aria-label={t('Synchronized')}
          />
        )
        if (!key.cost_available) {
          statusContent = (
            <Badge
              variant='warning'
              className='h-4 px-1.5 py-0 text-[10px] font-normal'
            >
              {t('Partial data')}
            </Badge>
          )
        }
        if (key.partial) {
          statusContent = (
            <Badge
              variant='warning'
              className='h-4 px-1.5 py-0 text-[10px] font-normal'
            >
              {t('Partial data')}
            </Badge>
          )
        }
        if (key.last_error) {
          statusContent = (
            <Tooltip>
              <TooltipTrigger
                render={
                  <Badge
                    variant='destructive'
                    aria-label={t('Sync failed')}
                    className='h-4 cursor-help px-1.5 py-0 text-[10px] font-normal'
                  >
                    {t('Sync failed')}
                  </Badge>
                }
              />
              <TooltipContent className='max-w-[260px] text-xs'>
                {key.last_error}
              </TooltipContent>
            </Tooltip>
          )
        }

        return (
          <div
            key={key.key_id}
            className='hover:bg-muted/20 flex min-h-8 w-full items-center py-1.5 text-xs transition-colors'
          >
            <div
              data-profit-detail-column='key'
              className='flex w-[40%] min-w-0 items-center gap-1.5 pr-2 pl-12 text-left'
            >
              <div
                data-profit-detail-account
                className='flex min-w-0 items-center gap-1.5'
              >
                <span
                  className='text-muted-foreground/40 shrink-0 font-mono text-xs select-none'
                  aria-hidden='true'
                >
                  ↳
                </span>
                <span className='text-foreground/80 min-w-0 truncate font-medium'>
                  {key.key_name || '-'}
                </span>
                {downstreamRatioSummary && (
                  <span className='text-muted-foreground shrink-0 font-mono text-[11px]'>
                    · {downstreamRatioSummary}
                  </span>
                )}
                {key.ratio_available && (
                  <span
                    data-profit-detail-upstream
                    className='text-muted-foreground shrink-0 font-mono text-[11px]'
                  >
                    · {ratioText(key.upstream_group_ratio)}
                  </span>
                )}
              </div>
            </div>

            <div
              data-profit-detail-column='revenue'
              className='text-foreground/80 w-[15%] px-2 text-center font-normal tabular-nums'
            >
              {key.revenue_available
                ? formatBillingCurrencyFromUSD(key.revenue_usd)
                : '-'}
            </div>
            <div
              data-profit-detail-column='cost'
              className='text-foreground/80 w-[15%] px-2 text-center font-normal tabular-nums'
            >
              {key.cost_available
                ? formatBillingCurrencyFromUSD(key.cost_usd)
                : '-'}
            </div>
            <div
              data-profit-detail-column='profit'
              className={cn(
                'w-[15%] px-2 text-center font-normal tabular-nums',
                key.profit_available && key.profit_usd < 0
                  ? 'text-destructive'
                  : key.profit_available && 'text-success'
              )}
            >
              {key.profit_available
                ? formatBillingCurrencyFromUSD(key.profit_usd)
                : '-'}
            </div>
            <div
              data-profit-detail-column='status'
              className='flex w-[15%] items-center justify-center px-2 text-center'
            >
              {statusContent}
            </div>
          </div>
        )
      })}
    </div>
  )
}

export function ProfitTable(props: ProfitTableProps) {
  const { t } = useTranslation()
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())
  const [settingsRow, setSettingsRow] = useState<ChannelProfitRow>()

  const statusLabel: Record<ChannelProfitStatus, string> = {
    disabled: t('Monitoring disabled'),
    pending: t('Waiting for first sync'),
    synced: t('Synchronized'),
    partial: t('Partial data'),
    error: t('Sync failed'),
  }

  const toggleExpanded = useCallback((groupId: string) => {
    setExpandedGroups((current) => {
      const next = new Set(current)
      if (next.has(groupId)) {
        next.delete(groupId)
      } else {
        next.add(groupId)
      }
      return next
    })
  }, [])

  if (props.rows.length === 0) {
    return (
      <EmptyState
        icon={CircleDollarSign}
        title={t('No monitored channels')}
        bordered
      />
    )
  }

  return (
    <TooltipProvider>
      <div className='bg-card overflow-x-auto rounded-xl border shadow-2xs'>
        <Table className='min-w-[980px] table-fixed'>
          <TableHeader>
            <TableRow className='bg-muted/30 hover:bg-muted/30 border-b'>
              <TableHead className='h-9 w-[40%] px-4' />
              <TableHead className='text-muted-foreground h-9 w-[15%] text-center text-xs font-semibold'>
                {t('Revenue')}
              </TableHead>
              <TableHead className='text-muted-foreground h-9 w-[15%] text-center text-xs font-semibold'>
                {t('Expense')}
              </TableHead>
              <TableHead className='text-muted-foreground h-9 w-[15%] text-center text-xs font-semibold'>
                {t('Profit')}
              </TableHead>
              <TableHead className='text-muted-foreground h-9 w-[15%] text-center text-xs font-semibold'>
                {t('Status')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.rows.map((row) => {
              const expanded = expandedGroups.has(row.group_id)
              const pLabel = providerLabel(row.provider)
              const isSyncing = props.syncingChannelId === row.channel_id
              let syncTooltip = t('Sync this channel')
              if (!row.enabled) {
                syncTooltip = t('Enable monitoring to sync')
              } else if (isSyncing) {
                syncTooltip = t('Syncing...')
              }

              return (
                <Fragment key={row.group_id}>
                  <TableRow
                    data-profit-group-header={row.group_id}
                    className={cn(
                      'transition-colors hover:bg-muted/20',
                      expanded && 'bg-muted/15'
                    )}
                    aria-expanded={expanded}
                  >
                    <TableCell className='px-4 py-3'>
                      <div className='flex min-w-0 items-start gap-2.5'>
                        <Button
                          type='button'
                          variant='ghost'
                          size='icon-xs'
                          className='hover:bg-muted mt-0.5 size-6 rounded-md'
                          onClick={() => toggleExpanded(row.group_id)}
                          aria-label={expanded ? t('Collapse') : t('Expand')}
                          aria-expanded={expanded}
                        >
                          <HugeiconsIcon
                            icon={ArrowRight01Icon}
                            strokeWidth={2.2}
                            aria-hidden='true'
                            className={cn(
                              'size-3.5 text-muted-foreground transition-transform duration-200',
                              expanded && 'rotate-90 text-foreground'
                            )}
                          />
                        </Button>
                        <div className='min-w-0'>
                          <div className='flex min-w-0 items-center gap-1'>
                            {props.isRoot ? (
                              <button
                                type='button'
                                className='text-foreground/90 hover:text-primary min-w-0 cursor-pointer truncate text-left text-sm font-semibold transition-colors hover:underline'
                                onClick={() => setSettingsRow(row)}
                                aria-label={t('Settings')}
                              >
                                {row.channel_name}
                              </button>
                            ) : (
                              <p className='text-foreground/90 min-w-0 truncate text-sm font-semibold'>
                                {row.channel_name}
                              </p>
                            )}
                            {props.isRoot && (
                              <Tooltip>
                                <TooltipTrigger
                                  render={
                                    <Button
                                      type='button'
                                      variant='ghost'
                                      size='icon-xs'
                                      className='text-muted-foreground hover:text-foreground size-5 shrink-0 rounded-md'
                                      onClick={() =>
                                        props.onSync(row.channel_id)
                                      }
                                      disabled={!row.enabled || isSyncing}
                                      aria-label={t('Sync this channel')}
                                    >
                                      <HugeiconsIcon
                                        icon={RefreshIcon}
                                        strokeWidth={2}
                                        aria-hidden='true'
                                        className={cn(
                                          'size-3.5',
                                          isSyncing && 'animate-spin'
                                        )}
                                      />
                                    </Button>
                                  }
                                />
                                <TooltipContent>{syncTooltip}</TooltipContent>
                              </Tooltip>
                            )}
                          </div>
                          <p
                            className='text-muted-foreground mt-0.5 max-w-[560px] truncate font-mono text-[11px]'
                            title={row.base_url}
                          >
                            {row.base_url}
                          </p>
                          {/* 优化点 3：统一融合成一条极简文本，去掉了突兀的圆角框 */}
                          <p className='text-muted-foreground mt-1.5 truncate text-xs'>
                            {t('{{count}} local channels', {
                              count: row.channel_ids.length,
                            })}{' '}
                            ·{' '}
                            {t('{{count}} upstream keys', {
                              count: row.keys.length,
                            })}
                            {pLabel && ` · ${pLabel}`}
                          </p>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className='text-foreground/90 py-3 text-center font-semibold tabular-nums'>
                      {formatBillingCurrencyFromUSD(row.revenue_usd)}
                    </TableCell>
                    <TableCell className='text-muted-foreground py-3 text-center tabular-nums'>
                      {row.cost_available
                        ? formatBillingCurrencyFromUSD(row.cost_usd)
                        : '-'}
                    </TableCell>
                    <TableCell
                      className={cn(
                        'py-3 text-center font-semibold tabular-nums',
                        row.profit_available && row.profit_usd < 0
                          ? 'text-destructive'
                          : row.profit_available && 'text-success'
                      )}
                    >
                      {row.profit_available
                        ? formatBillingCurrencyFromUSD(row.profit_usd)
                        : '-'}
                      {row.margin_available && (
                        <span className='text-muted-foreground ml-1 text-[11px] font-normal'>
                          ({(row.margin * 100).toFixed(1)}%)
                        </span>
                      )}
                    </TableCell>
                    <TableCell className='py-3 text-center'>
                      <div className='flex flex-col items-center justify-center gap-0.5'>
                        <Badge
                          variant={STATUS_VARIANT[row.status]}
                          className='py-0.5 text-[11px] font-normal'
                        >
                          {statusLabel[row.status]}
                        </Badge>
                        {row.last_error && (
                          <p
                            className='text-destructive mt-1 max-w-[180px] truncate text-[11px]'
                            title={row.last_error}
                          >
                            {row.last_error}
                          </p>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                  {expanded && (
                    <TableRow className='border-border/40 border-b p-0 hover:bg-transparent'>
                      <TableCell colSpan={5} className='p-0'>
                        <ProfitSubRows row={row} />
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
          </TableBody>
        </Table>
      </div>
      {settingsRow && (
        <ProfitSettingsDialog
          key={settingsRow.group_id}
          row={settingsRow}
          saving={props.savingChannelId === settingsRow.channel_id}
          onClose={() => setSettingsRow(undefined)}
          onSave={props.onSaveSettings}
        />
      )}
    </TooltipProvider>
  )
}

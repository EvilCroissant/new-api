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
  ArrowRight01Icon,
  RefreshIcon,
  Settings02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { CircleDollarSign } from 'lucide-react'
import { Fragment, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
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

type ProfitTableProps = {
  rows: ChannelProfitRow[]
  isRoot: boolean
  togglingChannelId?: number
  syncingChannelId?: number
  savingChannelId?: number
  onToggle: (channelId: number, enabled: boolean) => void
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
  if (provider === 'new_api') return 'New API'
  if (provider === 'sub2api') return 'Sub2API'
  if (provider === 'mixed') return 'New API / Sub2API'
  return ''
}

function uniqueUpstreamRates(row: ChannelProfitRow) {
  const seen = new Set<string>()
  return row.keys.filter((key) => {
    if (!key.ratio_available) return false
    const identity = `${key.upstream_group}:${key.upstream_group_ratio}`
    if (seen.has(identity)) return false
    seen.add(identity)
    return true
  })
}

function ProfitKeyDetails(props: { row: ChannelProfitRow }) {
  const { t } = useTranslation()

  return (
    <div className='divide-border divide-y'>
      <div className='text-muted-foreground grid grid-cols-[minmax(180px,1.3fr)_minmax(150px,1fr)_100px_minmax(140px,1fr)_110px_100px_110px] gap-3 px-3 py-2 text-xs font-medium'>
        <span>{t('Upstream key')}</span>
        <span>{t('Local channels')}</span>
        <span>{t('Backend')}</span>
        <span>{t('Upstream group')}</span>
        <span>{t('Upstream ratio')}</span>
        <span className='text-right'>{t('Cost')}</span>
        <span className='text-right'>{t('Status')}</span>
      </div>
      {props.row.keys.map((key) => (
        <div
          key={key.key_id}
          className='grid grid-cols-[minmax(180px,1.3fr)_minmax(150px,1fr)_100px_minmax(140px,1fr)_110px_100px_110px] items-center gap-3 px-3 py-2.5 text-xs'
        >
          <div className='min-w-0'>
            <p className='truncate font-medium'>{key.key_name || '-'}</p>
            <code className='text-muted-foreground'>{key.key_hint}</code>
          </div>
          <div className='flex min-w-0 flex-wrap gap-1'>
            {key.channel_names.map((name, index) => (
              <Badge
                key={`${key.channel_ids[index]}:${name}`}
                variant='outline'
              >
                #{key.channel_ids[index]} {name}
              </Badge>
            ))}
          </div>
          <span>{providerLabel(key.provider) || '-'}</span>
          <span className='truncate'>{key.upstream_group || '-'}</span>
          <span>
            {key.ratio_available
              ? ratioText(key.upstream_group_ratio)
              : t('Unknown ratio')}
          </span>
          <span className='text-right tabular-nums'>
            {key.cost_available
              ? formatBillingCurrencyFromUSD(key.cost_usd)
              : '-'}
          </span>
          <span
            className={cn(
              'truncate text-right',
              key.last_error && 'text-destructive'
            )}
            title={key.last_error}
          >
            {key.last_error ? t('Sync failed') : t('Synchronized')}
          </span>
        </div>
      ))}
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

  if (props.rows.length === 0) {
    return (
      <EmptyState
        icon={CircleDollarSign}
        title={t('No monitored channels')}
        bordered
      />
    )
  }

  const toggleExpanded = (groupId: string) => {
    setExpandedGroups((current) => {
      const next = new Set(current)
      if (next.has(groupId)) next.delete(groupId)
      else next.add(groupId)
      return next
    })
  }

  return (
    <TooltipProvider>
      <div className='overflow-x-auto rounded-lg border'>
        <Table className='min-w-[1240px]'>
          <TableHeader>
            <TableRow className='bg-muted/40 hover:bg-muted/40'>
              <TableHead className='h-9 w-[220px] px-4 text-xs'>
                {t('Channel')}
              </TableHead>
              <TableHead className='h-9 w-[155px] text-xs'>
                {t('Channel ratio')}
              </TableHead>
              <TableHead className='h-9 w-[145px] text-xs'>
                {t('Upstream keys')}
              </TableHead>
              <TableHead className='h-9 min-w-[175px] text-xs'>
                {t('Upstream ratio')}
              </TableHead>
              <TableHead className='h-9 w-[90px] text-right text-xs'>
                {t('Revenue')}
              </TableHead>
              <TableHead className='h-9 w-[90px] text-right text-xs'>
                {t('Cost')}
              </TableHead>
              <TableHead className='h-9 w-[115px] text-right text-xs'>
                {t('Profit')}
              </TableHead>
              <TableHead className='h-9 w-[120px] text-right text-xs'>
                {t('Status')}
              </TableHead>
              <TableHead className='h-9 w-[120px] pr-4 text-right text-xs'>
                {t('Actions')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.rows.map((row) => {
              const expanded = expandedGroups.has(row.group_id)
              const upstreamRates = uniqueUpstreamRates(row)
              return (
                <Fragment key={row.group_id}>
                  <TableRow
                    className='hover:bg-muted/30'
                    aria-expanded={expanded}
                  >
                    <TableCell className='px-4 py-3 align-top'>
                      <div className='flex items-start gap-2'>
                        <Button
                          type='button'
                          variant='ghost'
                          size='icon-xs'
                          className='mt-0.5'
                          onClick={() => toggleExpanded(row.group_id)}
                          aria-label={expanded ? t('Collapse') : t('Expand')}
                          aria-expanded={expanded}
                        >
                          <HugeiconsIcon
                            icon={ArrowRight01Icon}
                            strokeWidth={2}
                            aria-hidden='true'
                            className={cn(
                              'transition-transform',
                              expanded && 'rotate-90'
                            )}
                          />
                        </Button>
                        <div className='min-w-0'>
                          <p className='truncate font-medium'>
                            {row.channel_name}
                          </p>
                          <p
                            className='text-muted-foreground mt-1 max-w-[170px] truncate font-mono text-[11px]'
                            title={row.base_url}
                          >
                            {row.base_url}
                          </p>
                          <div className='mt-1 flex flex-wrap gap-1'>
                            {row.channel_ids.map((id) => (
                              <Badge key={id} variant='outline'>
                                #{id}
                              </Badge>
                            ))}
                            {providerLabel(row.provider) && (
                              <Badge variant='outline'>
                                {providerLabel(row.provider)}
                              </Badge>
                            )}
                          </div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className='py-3 align-top'>
                      {row.downstream_rates.length > 0 ? (
                        <div className='flex flex-wrap gap-1.5'>
                          {row.downstream_rates.map((rate) => (
                            <Badge key={rate.group} variant='outline'>
                              {rate.group} {ratioText(rate.ratio)}
                            </Badge>
                          ))}
                        </div>
                      ) : (
                        <span className='text-muted-foreground'>-</span>
                      )}
                    </TableCell>
                    <TableCell className='py-3 align-top'>
                      <button
                        type='button'
                        className='hover:text-foreground text-muted-foreground text-left text-xs underline-offset-4 hover:underline'
                        onClick={() => toggleExpanded(row.group_id)}
                      >
                        {t('{{count}} upstream keys', {
                          count: row.keys.length,
                        })}
                      </button>
                    </TableCell>
                    <TableCell className='py-3 align-top'>
                      {upstreamRates.length > 0 ? (
                        <div className='flex flex-wrap gap-1.5'>
                          {upstreamRates.map((key) => (
                            <Badge key={key.key_id} variant='outline'>
                              {key.upstream_group || t('Unknown group')}{' '}
                              {ratioText(key.upstream_group_ratio)}
                            </Badge>
                          ))}
                        </div>
                      ) : (
                        <span className='text-muted-foreground'>-</span>
                      )}
                    </TableCell>
                    <TableCell className='py-3 text-right align-top font-medium tabular-nums'>
                      {formatBillingCurrencyFromUSD(row.revenue_usd)}
                    </TableCell>
                    <TableCell className='py-3 text-right align-top tabular-nums'>
                      {row.cost_available
                        ? formatBillingCurrencyFromUSD(row.cost_usd)
                        : '-'}
                    </TableCell>
                    <TableCell
                      className={cn(
                        'py-3 text-right align-top font-medium tabular-nums',
                        row.profit_available && row.profit_usd < 0
                          ? 'text-destructive'
                          : row.profit_available &&
                              'text-emerald-600 dark:text-emerald-400'
                      )}
                    >
                      {row.profit_available
                        ? formatBillingCurrencyFromUSD(row.profit_usd)
                        : '-'}
                      {row.margin_available && (
                        <span className='text-muted-foreground ml-1 text-[11px]'>
                          {(row.margin * 100).toFixed(1)}%
                        </span>
                      )}
                    </TableCell>
                    <TableCell className='py-3 text-right align-top'>
                      <Badge variant={STATUS_VARIANT[row.status]}>
                        {statusLabel[row.status]}
                      </Badge>
                      {row.last_error && (
                        <p
                          className='text-destructive mt-1 ml-auto max-w-[180px] truncate text-[11px]'
                          title={row.last_error}
                        >
                          {row.last_error}
                        </p>
                      )}
                    </TableCell>
                    <TableCell className='py-3 pr-4 align-top'>
                      {props.isRoot && (
                        <div className='flex justify-end gap-1'>
                          <Tooltip>
                            <TooltipTrigger
                              render={
                                <Button
                                  type='button'
                                  variant='ghost'
                                  size='icon-sm'
                                  onClick={() => props.onSync(row.channel_id)}
                                  disabled={
                                    !row.enabled ||
                                    props.syncingChannelId === row.channel_id
                                  }
                                  aria-label={t('Sync this channel')}
                                />
                              }
                            >
                              <HugeiconsIcon
                                icon={RefreshIcon}
                                strokeWidth={2}
                                aria-hidden='true'
                                className={cn(
                                  props.syncingChannelId === row.channel_id &&
                                    'animate-spin'
                                )}
                              />
                            </TooltipTrigger>
                            <TooltipContent>
                              {t('Sync this channel')}
                            </TooltipContent>
                          </Tooltip>
                          {row.provider === 'new_api' && (
                            <Tooltip>
                              <TooltipTrigger
                                render={
                                  <Button
                                    type='button'
                                    variant='ghost'
                                    size='icon-sm'
                                    onClick={() => setSettingsRow(row)}
                                    aria-label={t('Settings')}
                                  />
                                }
                              >
                                <HugeiconsIcon
                                  icon={Settings02Icon}
                                  strokeWidth={2}
                                  aria-hidden='true'
                                />
                              </TooltipTrigger>
                              <TooltipContent>{t('Settings')}</TooltipContent>
                            </Tooltip>
                          )}
                          <Switch
                            size='sm'
                            checked={row.enabled}
                            disabled={
                              props.togglingChannelId === row.channel_id
                            }
                            onCheckedChange={(enabled) =>
                              props.onToggle(row.channel_id, enabled)
                            }
                            aria-label={
                              row.enabled
                                ? t('Disable monitoring')
                                : t('Enable monitoring')
                            }
                          />
                        </div>
                      )}
                    </TableCell>
                  </TableRow>
                  {expanded && (
                    <TableRow className='bg-muted/20 hover:bg-muted/20'>
                      <TableCell
                        colSpan={9}
                        className='px-10 py-2 whitespace-normal'
                      >
                        <ProfitKeyDetails row={row} />
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

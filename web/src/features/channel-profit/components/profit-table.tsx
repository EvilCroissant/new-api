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

import { CircleDollarSign } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { cn } from '@/lib/utils'

import type {
  ChannelProfitProvider,
  ChannelProfitRow,
  ChannelProfitStatus,
} from '../types'

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
  onToggle: (channelId: number, enabled: boolean) => void
}

function ratioText(ratio: number) {
  return `${ratio.toFixed(4).replace(/0+$/, '').replace(/\.$/, '')}x`
}

function providerLabel(provider: ChannelProfitProvider) {
  if (provider === 'new_api') return 'New API'
  if (provider === 'sub2api') return 'Sub2API'
  return ''
}

export function ProfitTable(props: ProfitTableProps) {
  const { t } = useTranslation()
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

  return (
    <div className='overflow-x-auto rounded-lg border'>
      <Table className='min-w-[960px]'>
        <TableHeader>
          <TableRow className='bg-muted/40 hover:bg-muted/40'>
            <TableHead className='h-9 w-[180px] px-4 text-xs'>
              {t('Channel')}
            </TableHead>
            <TableHead className='h-9 w-[140px] text-xs'>
              {t('Downstream ratio')}
            </TableHead>
            <TableHead className='h-9 min-w-[240px] text-xs'>
              {t('Upstream keys and ratios')}
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
            <TableHead className='h-9 w-[120px] pr-4 text-right text-xs'>
              {t('Status')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.rows.map((row) => (
            <TableRow key={row.channel_id} className='hover:bg-muted/30'>
              <TableCell className='px-4 py-3 align-top'>
                <div className='flex items-start justify-between gap-3'>
                  <div className='min-w-0'>
                    <p className='truncate font-medium'>{row.channel_name}</p>
                    <div className='mt-1 flex flex-wrap items-center gap-1.5'>
                      <span className='text-muted-foreground font-mono text-[11px]'>
                        #{row.channel_id}
                      </span>
                      {providerLabel(row.provider) && (
                        <Badge
                          variant='outline'
                          className='px-1.5 py-0 text-[10px]'
                        >
                          {providerLabel(row.provider)}
                        </Badge>
                      )}
                    </div>
                  </div>
                  {props.isRoot && (
                    <Switch
                      size='sm'
                      checked={row.enabled}
                      disabled={props.togglingChannelId === row.channel_id}
                      onCheckedChange={(enabled) =>
                        props.onToggle(row.channel_id, enabled)
                      }
                      aria-label={
                        row.enabled
                          ? t('Disable monitoring')
                          : t('Enable monitoring')
                      }
                    />
                  )}
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
                {row.keys.length > 0 ? (
                  <div className='space-y-2'>
                    {row.keys.map((key) => (
                      <div
                        key={key.key_id}
                        className='flex flex-wrap items-center gap-x-2 gap-y-1 text-xs'
                      >
                        <code className='bg-muted rounded px-1.5 py-0.5'>
                          {key.key_hint}
                        </code>
                        {key.upstream_group && (
                          <span>{key.upstream_group}</span>
                        )}
                        {!key.upstream_group && key.provider !== 'sub2api' && (
                          <span>{t('Unknown group')}</span>
                        )}
                        <Badge variant='outline'>
                          {key.ratio_available
                            ? ratioText(key.upstream_group_ratio)
                            : t('Unknown ratio')}
                        </Badge>
                        <span className='text-muted-foreground tabular-nums'>
                          {key.cost_available
                            ? formatBillingCurrencyFromUSD(key.cost_usd)
                            : '-'}
                        </span>
                      </div>
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
              <TableCell className='py-3 pr-4 text-right align-top'>
                <Badge variant={STATUS_VARIANT[row.status]}>
                  {statusLabel[row.status]}
                </Badge>
                {row.last_error && (
                  <p
                    className='text-destructive mt-1 ml-auto max-w-[220px] truncate text-[11px]'
                    title={row.last_error}
                  >
                    {row.last_error}
                  </p>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

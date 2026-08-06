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
  Analytics01Icon,
  ArrowRight01Icon,
  MoreHorizontalIcon,
  Plug01Icon,
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Separator } from '@/components/ui/separator'
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
  testingChannelId?: number
  onToggle: (channelId: number, enabled: boolean) => void
  onSync: (channelId: number) => void
  onViewLogs: (channelId: number) => void
  onTest: (channelId: number, channelName: string) => void
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

function ProfitKeyDetails(props: { row: ChannelProfitRow }) {
  const { t } = useTranslation()

  return (
    <div
      data-profit-group-details={props.row.group_id}
      className='bg-background flex flex-col gap-3 rounded-md border p-4 shadow-xs'
    >
      <div className='flex min-w-0 flex-wrap items-center justify-between gap-2'>
        <h4 className='min-w-0 truncate text-sm font-semibold'>
          {props.row.channel_name} · {t('Channel and upstream key details')}
        </h4>
        <span className='text-muted-foreground shrink-0 text-xs'>
          {t('{{count}} upstream keys', { count: props.row.keys.length })}
        </span>
      </div>
      <Separator />
      <div className='overflow-x-auto rounded-md border'>
        <Table className='min-w-[900px] table-fixed text-xs'>
          <TableHeader>
            <TableRow className='bg-muted/40 hover:bg-muted/40'>
              <TableHead className='h-9 w-[24%] px-3 text-xs'>
                {t('Upstream key')}
              </TableHead>
              <TableHead className='h-9 w-[27%] px-3 text-xs'>
                {t('Local channels')}
              </TableHead>
              <TableHead className='h-9 w-[25%] px-3 text-xs'>
                {t('Downstream ratio')}
              </TableHead>
              <TableHead className='h-9 w-[12%] px-3 text-xs'>
                {t('Upstream ratio')}
              </TableHead>
              <TableHead className='h-9 w-[12%] px-3 text-right text-xs'>
                {t('Cost')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.row.keys.map((key) => (
              <TableRow key={key.key_id} className='hover:bg-muted/20'>
                <TableCell className='px-3 py-2.5 align-top'>
                  <div className='flex min-w-0 flex-col gap-1'>
                    <p className='truncate font-medium'>{key.key_name || '-'}</p>
                    <code className='text-muted-foreground truncate'>
                      {key.key_hint}
                    </code>
                    <div className='flex flex-wrap gap-1'>
                      {providerLabel(key.provider) && (
                        <Badge variant='outline'>
                          {providerLabel(key.provider)}
                        </Badge>
                      )}
                      {key.upstream_group && (
                        <Badge variant='secondary'>{key.upstream_group}</Badge>
                      )}
                    </div>
                    {key.last_error && (
                      <p
                        className='text-destructive truncate text-[11px]'
                        title={key.last_error}
                      >
                        {key.last_error}
                      </p>
                    )}
                  </div>
                </TableCell>
                <TableCell className='px-3 py-2.5 align-top'>
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
                </TableCell>
                <TableCell className='px-3 py-2.5 align-top'>
                  {(key.downstream_rates ?? []).length > 0 ? (
                    <div className='flex flex-wrap gap-1'>
                      {(key.downstream_rates ?? []).map((rate) => (
                        <Badge key={rate.group} variant='outline'>
                          {rate.group} {ratioText(rate.ratio)}
                        </Badge>
                      ))}
                    </div>
                  ) : (
                    <span className='text-muted-foreground'>-</span>
                  )}
                </TableCell>
                <TableCell className='px-3 py-2.5 align-top tabular-nums'>
                  {key.ratio_available
                    ? ratioText(key.upstream_group_ratio)
                    : t('Unknown ratio')}
                </TableCell>
                <TableCell className='px-3 py-2.5 text-right align-top font-medium tabular-nums'>
                  {key.cost_available
                    ? formatBillingCurrencyFromUSD(key.cost_usd)
                    : '-'}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

function ProfitRowMenu(props: {
  row: ChannelProfitRow
  testingChannelId?: number
  onSettings: () => void
  onViewLogs: (channelId: number) => void
  onTest: (channelId: number, channelName: string) => void
}) {
  const { t } = useTranslation()
  const hasMultipleChannels = props.row.channel_ids.length > 1

  const channelItems = props.row.channel_ids.map((channelId, index) => ({
    id: channelId,
    name: props.row.channel_names?.[index] || `#${channelId}`,
  }))

  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        render={
          <Button
            type='button'
            variant='ghost'
            size='icon-xs'
            aria-label={t('Open menu')}
          />
        }
      >
        <HugeiconsIcon
          icon={MoreHorizontalIcon}
          strokeWidth={2}
          aria-hidden='true'
        />
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' className='w-52'>
        <DropdownMenuGroup>
          <DropdownMenuItem onClick={props.onSettings}>
            <HugeiconsIcon
              icon={Settings02Icon}
              strokeWidth={2}
              aria-hidden='true'
            />
            {t('Settings')}
          </DropdownMenuItem>
          {hasMultipleChannels ? (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <HugeiconsIcon
                  icon={Analytics01Icon}
                  strokeWidth={2}
                  aria-hidden='true'
                />
                {t('Usage logs')}
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className='w-60'>
                <DropdownMenuGroup>
                  {channelItems.map((channel) => (
                    <DropdownMenuItem
                      key={channel.id}
                      onClick={() => props.onViewLogs(channel.id)}
                    >
                      <span className='truncate'>
                        #{channel.id} {channel.name}
                      </span>
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuGroup>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          ) : (
            <DropdownMenuItem
              onClick={() => props.onViewLogs(channelItems[0].id)}
            >
              <HugeiconsIcon
                icon={Analytics01Icon}
                strokeWidth={2}
                aria-hidden='true'
              />
              {t('Usage logs')}
            </DropdownMenuItem>
          )}
          {hasMultipleChannels ? (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <HugeiconsIcon
                  icon={Plug01Icon}
                  strokeWidth={2}
                  aria-hidden='true'
                />
                {t('Test Connection')}
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className='w-60'>
                <DropdownMenuGroup>
                  {channelItems.map((channel) => (
                    <DropdownMenuItem
                      key={channel.id}
                      disabled={props.testingChannelId === channel.id}
                      onClick={() => props.onTest(channel.id, channel.name)}
                    >
                      <span className='truncate'>
                        #{channel.id} {channel.name}
                      </span>
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuGroup>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          ) : (
            <DropdownMenuItem
              disabled={props.testingChannelId === channelItems[0].id}
              onClick={() =>
                props.onTest(channelItems[0].id, channelItems[0].name)
              }
            >
              <HugeiconsIcon
                icon={Plug01Icon}
                strokeWidth={2}
                aria-hidden='true'
              />
              {t('Test Connection')}
            </DropdownMenuItem>
          )}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
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
        <Table className='min-w-[1080px] table-fixed'>
          <TableHeader>
            <TableRow className='bg-muted/40 hover:bg-muted/40'>
              <TableHead className='h-9 w-[32%] px-4 text-xs'>
                {t('Channel / Site')}
              </TableHead>
              <TableHead className='h-9 w-[12%] text-right text-xs'>
                {t('Downstream revenue')}
              </TableHead>
              <TableHead className='h-9 w-[12%] text-right text-xs'>
                {t('Upstream cost')}
              </TableHead>
              <TableHead className='h-9 w-[16%] text-right text-xs'>
                {t('Net profit')}
              </TableHead>
              <TableHead className='h-9 w-[14%] text-right text-xs'>
                {t('Data sync')}
              </TableHead>
              <TableHead className='h-9 w-[14%] pr-4 text-right text-xs'>
                {t('Actions')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.rows.map((row) => {
              const expanded = expandedGroups.has(row.group_id)
              return (
                <Fragment key={row.group_id}>
                  <TableRow
                    data-profit-group-header={row.group_id}
                    className='hover:bg-muted/20'
                    aria-expanded={expanded}
                  >
                    <TableCell className='px-4 py-3'>
                      <div className='flex min-w-0 items-start gap-2'>
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
                          <div className='flex min-w-0 items-center'>
                            <p className='min-w-0 truncate font-medium'>
                              {row.channel_name}
                            </p>
                          </div>
                          <p
                            className='text-muted-foreground mt-0.5 max-w-[560px] truncate font-mono text-[11px]'
                            title={row.base_url}
                          >
                            {row.base_url}
                          </p>
                          <div className='text-muted-foreground mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs'>
                            <span>
                              {t('{{count}} local channels', {
                                count: row.channel_ids.length,
                              })}
                            </span>
                            <span>
                              {t('{{count}} upstream keys', {
                                count: row.keys.length,
                              })}
                            </span>
                            {providerLabel(row.provider) && (
                              <Badge variant='outline'>
                                {providerLabel(row.provider)}
                              </Badge>
                            )}
                          </div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className='py-3 text-right font-medium tabular-nums'>
                      {formatBillingCurrencyFromUSD(row.revenue_usd)}
                    </TableCell>
                    <TableCell className='py-3 text-right tabular-nums'>
                      {row.cost_available
                        ? formatBillingCurrencyFromUSD(row.cost_usd)
                        : '-'}
                    </TableCell>
                    <TableCell
                      className={cn(
                        'py-3 text-right font-medium tabular-nums',
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
                    <TableCell className='py-3 text-right'>
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
                    <TableCell
                      data-profit-monitoring-action={row.group_id}
                      className='py-3 pr-4 text-right'
                    >
                      {props.isRoot && (
                        <div className='flex items-center justify-end gap-1'>
                          <Tooltip>
                            <TooltipTrigger
                              render={
                                <Button
                                  type='button'
                                  variant='ghost'
                                  size='icon-xs'
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
                          <Tooltip>
                            <TooltipTrigger
                              render={
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
                              }
                            />
                            <TooltipContent>
                              {row.enabled
                                ? t('Disable monitoring')
                                : t('Enable monitoring')}
                            </TooltipContent>
                          </Tooltip>
                          <ProfitRowMenu
                            row={row}
                            testingChannelId={props.testingChannelId}
                            onSettings={() => setSettingsRow(row)}
                            onViewLogs={props.onViewLogs}
                            onTest={props.onTest}
                          />
                        </div>
                      )}
                    </TableCell>
                  </TableRow>
                  {expanded && (
                    <TableRow className='bg-muted/20 hover:bg-muted/20'>
                      <TableCell
                        colSpan={6}
                        className='bg-muted/10 px-8 py-3 whitespace-normal sm:px-12'
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

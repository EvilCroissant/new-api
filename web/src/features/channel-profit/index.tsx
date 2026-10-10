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
  Add01Icon,
  RefreshIcon,
  Settings02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  UpstreamManagementLayout,
  type UpstreamManagementPageProps,
} from '@/features/upstream-management/components/upstream-management-layout'
import {
  createUpstreamMonitor,
  deleteUpstreamMonitor,
  listUpstreamMonitors,
  syncUpstreamMonitor,
  updateUpstreamMonitor,
} from '@/features/upstream-monitor/api'
import { UpstreamMonitorAddDialog } from '@/features/upstream-monitor/components/upstream-monitor-add-dialog'
import { UpstreamMonitorTable } from '@/features/upstream-monitor/components/upstream-monitor-table'
import type {
  UpstreamMonitor,
  UpstreamMonitorProvider,
  UpstreamMonitorUpdateInput,
} from '@/features/upstream-monitor/types'
import { formatTimestampRelative } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  getChannelProfit,
  syncChannelProfit,
  syncChannelProfitGroup,
  updateChannelProfitConfig,
  updateChannelProfitMonitoring,
} from './api'
import { ProfitSummaryCards } from './components/profit-summary-cards'
import { ProfitTable } from './components/profit-table'
import { SyncMonitoringDialog } from './components/sync-monitoring-dialog'
import type { ChannelProfitConfigInput } from './types'

const PROFIT_REFRESH_INTERVAL_MS = 30 * 1000

function getErrorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback
}

function upstreamSite(baseURL: string): string {
  try {
    const url = new URL(baseURL)
    return `${url.protocol}//${url.host}${url.pathname.replace(/\/+$/, '').replace(/\/v1$/, '')}`
  } catch {
    return baseURL
  }
}

export function ChannelProfit(props: UpstreamManagementPageProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [usageDate, setUsageDate] = useState(() => dayjs().format('YYYY-MM-DD'))
  const [syncSettingsOpen, setSyncSettingsOpen] = useState(false)
  const [addMonitorOpen, setAddMonitorOpen] = useState(false)
  const todayStr = dayjs().format('YYYY-MM-DD')

  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )

  const queryKey = ['channel-profit', usageDate] as const

  const profitQuery = useQuery({
    queryKey,
    queryFn: async () => {
      const response = await getChannelProfit(usageDate)
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Could not load profit data'))
      }
      return response.data
    },
    retry: false,
    staleTime: 15 * 1000,
    refetchInterval: PROFIT_REFRESH_INTERVAL_MS,
  })

  const monitorQuery = useQuery({
    queryKey: ['upstream-monitors'],
    queryFn: async () => {
      const response = requireServerSuccess(await listUpstreamMonitors())
      if (!response.data) throw new Error(t('Could not load upstream monitors'))
      return response.data
    },
    retry: false,
    refetchInterval: PROFIT_REFRESH_INTERVAL_MS,
  })

  const invalidateProfitQueries = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['channel-profit'] }),
      queryClient.invalidateQueries({ queryKey: ['upstream-monitors'] }),
    ])
  }, [queryClient])

  const monitoringMutation = useMutation({
    mutationFn: async (variables: { channelId: number; enabled: boolean }) => {
      const response = await updateChannelProfitMonitoring(
        variables.channelId,
        variables.enabled
      )
      if (!response.success) {
        throw new Error(response.message || t('Update failed'))
      }
      return variables
    },
    onSuccess: async (variables) => {
      toast.success(
        variables.enabled
          ? t('Profit monitoring enabled')
          : t('Profit monitoring disabled')
      )
      await invalidateProfitQueries()
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Update failed')))
    },
  })

  const syncMutation = useMutation({
    mutationFn: async (channelId?: number) => {
      const response = channelId
        ? await syncChannelProfitGroup(channelId)
        : await syncChannelProfit()
      if (!response.success) {
        throw new Error(response.message || t('Sync failed'))
      }
      return response
    },
    onSuccess: async () => {
      toast.success(t('Profit sync started'))
      await invalidateProfitQueries()
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Sync failed')))
    },
  })

  const configMutation = useMutation({
    mutationFn: async (variables: {
      channelId: number
      input: ChannelProfitConfigInput
      account?: UpstreamMonitorUpdateInput & {
        provider: UpstreamMonitorProvider
      }
    }) => {
      if (variables.account) {
        const row = profitQuery.data?.rows.find(
          (item) => item.channel_id === variables.channelId
        )
        if (!row || !monitorQuery.data) {
          throw new Error(t('Could not load upstream monitors'))
        }
        const monitors =
          queryClient.getQueryData<UpstreamMonitor[]>(['upstream-monitors']) ??
          monitorQuery.data
        const existing = monitors.find(
          (item) => upstreamSite(item.base_url) === upstreamSite(row.base_url)
        )
        const { provider, ...credentials } = variables.account
        const response = existing
          ? await updateUpstreamMonitor(existing.id, credentials)
          : await createUpstreamMonitor({
              ...credentials,
              access_token: credentials.access_token || '',
              provider,
              base_url: row.base_url,
              name: variables.input.display_name,
            })
        requireServerSuccess(response)
        if (!response.data) throw new Error(t('Credential update failed'))
        const saved = response.data
        // Keep the created account available if the following pricing save fails.
        queryClient.setQueryData<UpstreamMonitor[]>(
          ['upstream-monitors'],
          (current) => [
            ...(current ?? []).filter((item) => item.id !== saved.id),
            saved,
          ]
        )
        if (saved.last_error) {
          toast.warning(t('Credentials saved, but synchronization failed'))
        }
      }
      const response = await updateChannelProfitConfig(
        variables.channelId,
        variables.input
      )
      if (!response.success) {
        throw new Error(response.message || t('Update failed'))
      }
      return variables
    },
    onSuccess: async () => {
      toast.success(t('Profit settings updated'))
      await invalidateProfitQueries()
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Update failed')))
    },
  })

  const accountMutation = useMutation({
    mutationFn: async (variables: {
      id: number
      action: 'sync' | 'delete' | 'update'
      input?: UpstreamMonitorUpdateInput
    }) => {
      if (variables.action === 'sync') {
        requireServerSuccess(await syncUpstreamMonitor(variables.id))
      } else if (variables.action === 'delete') {
        requireServerSuccess(await deleteUpstreamMonitor(variables.id))
      } else {
        requireServerSuccess(
          await updateUpstreamMonitor(variables.id, variables.input ?? {})
        )
      }
    },
    onSuccess: invalidateProfitQueries,
    onError: (error) => toast.error(getErrorMessage(error, t('Update failed'))),
  })

  const summary = profitQuery.data
  const monitorsBySite = new Map(
    (monitorQuery.data ?? []).map((monitor) => [
      upstreamSite(monitor.base_url),
      monitor,
    ])
  )
  const rowMonitors: Record<number, UpstreamMonitor> = {}
  for (const row of summary?.rows ?? []) {
    const monitor = monitorsBySite.get(upstreamSite(row.base_url))
    if (monitor) rowMonitors[row.channel_id] = monitor
  }
  const profitSites = new Set(
    (summary?.rows ?? []).map((row) => upstreamSite(row.base_url))
  )
  const orphanMonitors = (monitorQuery.data ?? []).filter(
    (monitor) => !profitSites.has(upstreamSite(monitor.base_url))
  )
  const togglingChannelId = monitoringMutation.isPending
    ? monitoringMutation.variables?.channelId
    : undefined

  const syncingChannelId = syncMutation.isPending
    ? syncMutation.variables
    : undefined

  const savingChannelId = configMutation.isPending
    ? configMutation.variables?.channelId
    : undefined

  const actions = (
    <div className='flex flex-wrap items-center gap-2.5 max-sm:max-w-[calc(100vw-1.5rem)]'>
      {summary && (
        <div className='mr-1 flex items-center gap-2'>
          {summary.partial && (
            <Badge variant='warning' className='text-[11px] font-normal'>
              {t('Partial data')}
            </Badge>
          )}
          <span className='text-muted-foreground/80 hidden font-mono text-xs sm:inline-block'>
            {summary.last_synced_at > 0
              ? t('Last sync: {{time}}', {
                  time: formatTimestampRelative(summary.last_synced_at),
                })
              : t('Not synchronized yet')}
          </span>
        </div>
      )}
      <Input
        type='date'
        value={usageDate}
        max={todayStr}
        onChange={(event) => setUsageDate(event.target.value)}
        className='h-9 w-[140px] font-mono text-xs shadow-2xs'
        aria-label={t('Usage date')}
      />
      {isRoot && (
        <>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => setAddMonitorOpen(true)}
          >
            <HugeiconsIcon
              icon={Add01Icon}
              strokeWidth={2}
              data-icon='inline-start'
              aria-hidden='true'
            />
            {t('Add monitor')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='h-9 shadow-2xs'
            onClick={() => setSyncSettingsOpen(true)}
          >
            <HugeiconsIcon
              icon={Settings02Icon}
              strokeWidth={2}
              data-icon='inline-start'
              className='size-3.5'
              aria-hidden='true'
            />
            {t('Sync settings')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='h-9 shadow-2xs'
            onClick={() => syncMutation.mutate(undefined)}
            disabled={syncMutation.isPending}
          >
            <HugeiconsIcon
              icon={RefreshIcon}
              strokeWidth={2}
              data-icon='inline-start'
              className={cn(
                'size-3.5',
                syncMutation.isPending && 'animate-spin'
              )}
              aria-hidden='true'
            />
            {t('Sync now')}
          </Button>
        </>
      )}
    </div>
  )

  return (
    <UpstreamManagementLayout
      tab='profit'
      onTabChange={props.onTabChange}
      actions={actions}
    >
      {profitQuery.isLoading && (
        <div className='space-y-4'>
          <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
            {['revenue', 'cost', 'profit', 'margin'].map((metric) => (
              <Skeleton key={metric} className='h-20 rounded-xl' />
            ))}
          </div>
          <Skeleton className='h-[380px] rounded-xl' />
        </div>
      )}
      {!profitQuery.isLoading && (profitQuery.isError || !summary) && (
        <ErrorState
          title={t('Could not load profit data')}
          description={
            profitQuery.error instanceof Error
              ? profitQuery.error.message
              : undefined
          }
          onRetry={() => void profitQuery.refetch()}
        />
      )}
      {((!profitQuery.isLoading && !profitQuery.isError && summary) ||
        orphanMonitors.length > 0) && (
        <div className='space-y-4' aria-busy={profitQuery.isFetching}>
          {!profitQuery.isLoading && !profitQuery.isError && summary && (
            <ProfitSummaryCards summary={summary} />
          )}
          <ProfitTable
            rows={summary?.rows ?? []}
            accountRows={
              orphanMonitors.length > 0 ? (
                <UpstreamMonitorTable
                  embedded
                  monitors={orphanMonitors}
                  isRoot={isRoot}
                  syncingId={
                    accountMutation.isPending &&
                    accountMutation.variables.action === 'sync'
                      ? accountMutation.variables.id
                      : undefined
                  }
                  deletingId={
                    accountMutation.isPending &&
                    accountMutation.variables.action === 'delete'
                      ? accountMutation.variables.id
                      : undefined
                  }
                  updatingId={
                    accountMutation.isPending &&
                    accountMutation.variables.action === 'update'
                      ? accountMutation.variables.id
                      : undefined
                  }
                  onSync={(id) =>
                    accountMutation.mutate({ id, action: 'sync' })
                  }
                  onDelete={(id) =>
                    accountMutation.mutate({ id, action: 'delete' })
                  }
                  onUpdateCredentials={async (id, input) => {
                    await accountMutation.mutateAsync({
                      id,
                      input,
                      action: 'update',
                    })
                  }}
                />
              ) : undefined
            }
            monitors={rowMonitors}
            isRoot={isRoot && monitorQuery.isSuccess}
            syncingChannelId={syncingChannelId}
            savingChannelId={savingChannelId}
            onSync={(channelId) => syncMutation.mutate(channelId)}
            onSaveSettings={async (channelId, input, account) => {
              await configMutation.mutateAsync({ channelId, input, account })
            }}
          />
        </div>
      )}
      {monitorQuery.isError && (
        <ErrorState
          title={t('Could not load upstream monitors')}
          description={getErrorMessage(
            monitorQuery.error,
            t('Could not load upstream monitors')
          )}
          onRetry={() => void monitorQuery.refetch()}
        />
      )}
      <UpstreamMonitorAddDialog
        open={addMonitorOpen}
        onOpenChange={setAddMonitorOpen}
        onCreated={invalidateProfitQueries}
      />
      {syncSettingsOpen && summary && (
        <SyncMonitoringDialog
          rows={summary.rows}
          togglingChannelId={togglingChannelId}
          onToggle={(channelId, enabled) =>
            monitoringMutation.mutate({ channelId, enabled })
          }
          onClose={() => setSyncSettingsOpen(false)}
        />
      )}
    </UpstreamManagementLayout>
  )
}

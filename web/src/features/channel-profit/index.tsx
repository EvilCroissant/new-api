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

import { RefreshIcon, Settings02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { formatTimestampRelative } from '@/lib/format'
import { ROLE } from '@/lib/roles'
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

export function ChannelProfit() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [usageDate, setUsageDate] = useState(() => dayjs().format('YYYY-MM-DD'))
  const [syncSettingsOpen, setSyncSettingsOpen] = useState(false)
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

  const invalidateProfitQueries = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: ['channel-profit'] })
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
    }) => {
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

  const summary = profitQuery.data
  const togglingChannelId = monitoringMutation.isPending
    ? monitoringMutation.variables?.channelId
    : undefined

  const syncingChannelId = syncMutation.isPending
    ? syncMutation.variables
    : undefined

  const savingChannelId = configMutation.isPending
    ? configMutation.variables?.channelId
    : undefined

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Profit')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex flex-wrap items-center gap-2.5'>
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
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
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
        {!profitQuery.isLoading && !profitQuery.isError && summary && (
          <div className='space-y-4' aria-busy={profitQuery.isFetching}>
            <ProfitSummaryCards summary={summary} />
            <ProfitTable
              rows={summary.rows}
              isRoot={isRoot}
              syncingChannelId={syncingChannelId}
              savingChannelId={savingChannelId}
              onSync={(channelId) => syncMutation.mutate(channelId)}
              onSaveSettings={async (channelId, input) => {
                await configMutation.mutateAsync({ channelId, input })
              }}
            />
          </div>
        )}
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
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

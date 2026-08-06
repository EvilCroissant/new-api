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

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
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
  updateChannelProfitMonitoring,
} from './api'
import { ProfitSummaryCards } from './components/profit-summary-cards'
import { ProfitTable } from './components/profit-table'

const PROFIT_REFRESH_INTERVAL_MS = 30 * 1000

export function ChannelProfit() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [usageDate, setUsageDate] = useState(dayjs().format('YYYY-MM-DD'))
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const queryKey = ['channel-profit', usageDate]
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
      await queryClient.invalidateQueries({ queryKey: ['channel-profit'] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Update failed'))
    },
  })
  const syncMutation = useMutation({
    mutationFn: async () => {
      const response = await syncChannelProfit()
      if (!response.success) {
        throw new Error(response.message || t('Sync failed'))
      }
      return response
    },
    onSuccess: async () => {
      toast.success(t('Profit sync started'))
      await queryClient.invalidateQueries({ queryKey: ['channel-profit'] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Sync failed'))
    },
  })

  const summary = profitQuery.data
  const togglingChannelId = monitoringMutation.isPending
    ? monitoringMutation.variables?.channelId
    : undefined

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Profit')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Input
          type='date'
          value={usageDate}
          max={dayjs().format('YYYY-MM-DD')}
          onChange={(event) => setUsageDate(event.target.value)}
          className='w-[150px]'
          aria-label={t('Usage date')}
        />
        {isRoot && (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => syncMutation.mutate()}
            disabled={syncMutation.isPending}
          >
            <RefreshCw
              data-icon='inline-start'
              className={cn(
                'size-3.5',
                syncMutation.isPending && 'animate-spin'
              )}
              aria-hidden='true'
            />
            {t('Sync now')}
          </Button>
        )}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        {profitQuery.isLoading && (
          <div className='space-y-4'>
            <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
              {['revenue', 'cost', 'profit', 'margin'].map((metric) => (
                <Skeleton key={metric} className='h-20 rounded-lg' />
              ))}
            </div>
            <Skeleton className='h-[360px] rounded-lg' />
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
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div className='flex flex-wrap items-center gap-2'>
                {summary.partial && (
                  <Badge variant='warning'>{t('Partial data')}</Badge>
                )}
                <span className='text-muted-foreground text-xs'>
                  {summary.last_synced_at > 0
                    ? t('Last sync: {{time}}', {
                        time: formatTimestampRelative(summary.last_synced_at),
                      })
                    : t('Not synchronized yet')}
                </span>
              </div>
            </div>
            <ProfitSummaryCards summary={summary} />
            <ProfitTable
              rows={summary.rows}
              isRoot={isRoot}
              togglingChannelId={togglingChannelId}
              onToggle={(channelId, enabled) =>
                monitoringMutation.mutate({ channelId, enabled })
              }
            />
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

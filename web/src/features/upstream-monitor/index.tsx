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
import { Plus, RefreshCw } from 'lucide-react'
import { type ReactNode, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  deleteUpstreamMonitor,
  listUpstreamMonitors,
  syncUpstreamMonitor,
} from './api'
import { UpstreamMonitorAddDialog } from './components/upstream-monitor-add-dialog'
import { UpstreamMonitorDetailDialog } from './components/upstream-monitor-detail-dialog'
import { UpstreamMonitorTable } from './components/upstream-monitor-table'
import type { UpstreamMonitor } from './types'

function getErrorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback
}

export function UpstreamMonitorPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [isAddOpen, setIsAddOpen] = useState(false)
  const [detailsId, setDetailsId] = useState<number | null>(null)
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const monitorQuery = useQuery({
    queryKey: ['upstream-monitors'],
    queryFn: async () => {
      const response = await listUpstreamMonitors()
      if (!response.success || !response.data) {
        throw new Error(
          response.message || t('Could not load upstream monitors')
        )
      }
      return response.data
    },
    retry: false,
  })

  const refreshMonitors = async () => {
    await queryClient.invalidateQueries({ queryKey: ['upstream-monitors'] })
  }

  const syncMutation = useMutation({
    mutationFn: syncUpstreamMonitor,
    onSuccess: async (response, id) => {
      if (!response.success) {
        throw new Error(response.message || t('Sync failed'))
      }
      toast.success(t('Upstream monitor synchronized'))
      await Promise.all([
        refreshMonitors(),
        queryClient.invalidateQueries({ queryKey: ['upstream-monitor', id] }),
      ])
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Sync failed')))
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteUpstreamMonitor,
    onSuccess: async (response, id) => {
      if (!response.success) {
        throw new Error(response.message || t('Delete failed'))
      }
      if (detailsId === id) setDetailsId(null)
      toast.success(t('Upstream monitor deleted'))
      await refreshMonitors()
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Delete failed')))
    },
  })

  const monitors = monitorQuery.data ?? []
  const syncingId = syncMutation.isPending ? syncMutation.variables : undefined
  const deletingId = deleteMutation.isPending
    ? deleteMutation.variables
    : undefined
  let monitorContent: ReactNode
  if (monitorQuery.isLoading) {
    monitorContent = (
      <div className='space-y-2 rounded-xl border p-4'>
        <Skeleton className='h-8 w-full' />
        <Skeleton className='h-16 w-full' />
        <Skeleton className='h-16 w-full' />
      </div>
    )
  } else if (monitorQuery.isError) {
    monitorContent = (
      <ErrorState
        title={t('Could not load upstream monitors')}
        description={getErrorMessage(
          monitorQuery.error,
          t('Could not load upstream monitors')
        )}
        onRetry={() => monitorQuery.refetch()}
      />
    )
  } else {
    monitorContent = (
      <UpstreamMonitorTable
        monitors={monitors}
        isRoot={isRoot}
        syncingId={syncingId}
        deletingId={deletingId}
        onSync={(id) => syncMutation.mutate(id)}
        onDelete={(id) => deleteMutation.mutate(id)}
        onViewDetails={setDetailsId}
      />
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Upstream monitoring')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => monitorQuery.refetch()}
            disabled={monitorQuery.isFetching}
          >
            <RefreshCw
              data-icon='inline-start'
              className={
                monitorQuery.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'
              }
              aria-hidden='true'
            />
            {t('Refresh')}
          </Button>
          {isRoot && (
            <Button type='button' size='sm' onClick={() => setIsAddOpen(true)}>
              <Plus
                data-icon='inline-start'
                className='size-3.5'
                aria-hidden='true'
              />
              {t('Add monitor')}
            </Button>
          )}
        </div>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <>
          <div className='space-y-4'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Monitor independent upstream account balances, groups, and pricing.'
              )}
            </p>
            {monitorContent}
          </div>

          <UpstreamMonitorAddDialog
            open={isAddOpen}
            onOpenChange={setIsAddOpen}
            onCreated={async (_monitor: UpstreamMonitor) => {
              await refreshMonitors()
            }}
          />
          <UpstreamMonitorDetailDialog
            monitorId={detailsId}
            open={detailsId !== null}
            onOpenChange={(open) => !open && setDetailsId(null)}
          />
        </>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

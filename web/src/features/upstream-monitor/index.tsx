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

import { Add01Icon, RefreshIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
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
  updateUpstreamMonitor,
} from './api'
import { UpstreamMonitorAddDialog } from './components/upstream-monitor-add-dialog'
import { UpstreamMonitorTable } from './components/upstream-monitor-table'
import type { UpstreamMonitor, UpstreamMonitorUpdateInput } from './types'

function getErrorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback
}

export function UpstreamMonitorPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [isAddOpen, setIsAddOpen] = useState(false)
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
    onSuccess: async (response) => {
      if (!response.success) {
        throw new Error(response.message || t('Delete failed'))
      }
      toast.success(t('Upstream monitor deleted'))
      await refreshMonitors()
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Delete failed')))
    },
  })

  const updateMutation = useMutation({
    mutationFn: async ({
      id,
      input,
    }: {
      id: number
      input: UpstreamMonitorUpdateInput
    }) => {
      const response = await updateUpstreamMonitor(id, input)
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Credential update failed'))
      }
      return response.data
    },
    onSuccess: async (monitor) => {
      if (monitor.last_error) {
        toast.warning(t('Credentials saved, but synchronization failed'))
      } else {
        toast.success(t('Credentials updated'))
      }
      await refreshMonitors()
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Credential update failed')))
    },
  })

  const monitors = monitorQuery.data ?? []
  const syncingId = syncMutation.isPending ? syncMutation.variables : undefined
  const deletingId = deleteMutation.isPending
    ? deleteMutation.variables
    : undefined
  const updatingId = updateMutation.isPending
    ? updateMutation.variables.id
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
        updatingId={updatingId}
        onSync={(id) => syncMutation.mutate(id)}
        onDelete={(id) => deleteMutation.mutate(id)}
        onUpdateCredentials={async (id, input) => {
          await updateMutation.mutateAsync({ id, input })
        }}
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
            <HugeiconsIcon
              icon={RefreshIcon}
              strokeWidth={2}
              data-icon='inline-start'
              className={monitorQuery.isFetching ? 'animate-spin' : undefined}
              aria-hidden='true'
            />
            {t('Refresh')}
          </Button>
          {isRoot && (
            <Button type='button' size='sm' onClick={() => setIsAddOpen(true)}>
              <HugeiconsIcon
                icon={Add01Icon}
                strokeWidth={2}
                data-icon='inline-start'
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
                'Monitor independent upstream account balances and group pricing. Data refreshes automatically every hour.'
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
        </>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

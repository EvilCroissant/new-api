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

import { useQuery } from '@tanstack/react-query'
import { type ReactNode, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Spinner } from '@/components/ui/spinner'

import { getUpstreamMonitor } from '../api'

type UpstreamMonitorDetailDialogProps = {
  monitorId: number | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

function formatSnapshot(value: unknown): string {
  if (value === undefined || value === null) return ''
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return ''
  }
}

export function UpstreamMonitorDetailDialog(
  props: UpstreamMonitorDetailDialogProps
) {
  const { t } = useTranslation()
  const detailQuery = useQuery({
    queryKey: ['upstream-monitor', props.monitorId],
    queryFn: () =>
      props.monitorId ? getUpstreamMonitor(props.monitorId) : null,
    enabled: props.open && props.monitorId !== null,
    retry: false,
  })
  const monitor = detailQuery.data?.data
  const groupsJSON = useMemo(
    () => formatSnapshot(monitor?.groups),
    [monitor?.groups]
  )
  const pricingJSON = useMemo(
    () => formatSnapshot(monitor?.pricing),
    [monitor?.pricing]
  )
  let detailContent: ReactNode
  if (detailQuery.isLoading) {
    detailContent = (
      <div className='flex justify-center py-10'>
        <Spinner aria-label={t('Loading')} />
      </div>
    )
  } else if (detailQuery.isError || !detailQuery.data?.success) {
    detailContent = (
      <p className='text-destructive py-8 text-center text-sm'>
        {detailQuery.data?.message ||
          t('Could not load upstream monitor details')}
      </p>
    )
  } else {
    detailContent = (
      <>
        <section>
          <h3 className='mb-2 text-sm font-medium'>{t('Group snapshot')}</h3>
          {groupsJSON ? (
            <pre className='bg-muted max-h-72 overflow-auto rounded-lg border p-3 font-mono text-xs wrap-break-word whitespace-pre-wrap'>
              {groupsJSON}
            </pre>
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t('No group snapshot available.')}
            </p>
          )}
        </section>
        <section>
          <h3 className='mb-2 text-sm font-medium'>{t('Pricing snapshot')}</h3>
          {pricingJSON ? (
            <pre className='bg-muted max-h-72 overflow-auto rounded-lg border p-3 font-mono text-xs wrap-break-word whitespace-pre-wrap'>
              {pricingJSON}
            </pre>
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t('No pricing snapshot available.')}
            </p>
          )}
        </section>
      </>
    )
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='max-h-[calc(100dvh-2rem)] overflow-hidden sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('Upstream monitor details')}</DialogTitle>
          <DialogDescription className='truncate font-mono text-xs'>
            {monitor?.base_url || ''}
          </DialogDescription>
        </DialogHeader>

        <div className='max-h-[60vh] space-y-4 overflow-y-auto pr-1'>
          {detailContent}
        </div>

        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

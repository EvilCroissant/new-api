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

import { Folder01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import {
  formatGroupMultiplier,
  getGroupSnapshotItems,
} from '../lib/group-snapshot'
import type { UpstreamMonitor } from '../types'

type UpstreamMonitorGroupPanelProps = {
  monitor: UpstreamMonitor
  id: string
}

export function UpstreamMonitorGroupPanel(
  props: UpstreamMonitorGroupPanelProps
) {
  const { t } = useTranslation()
  const groups = useMemo(
    () => getGroupSnapshotItems(props.monitor.groups),
    [props.monitor.groups]
  )

  return (
    <section
      id={props.id}
      role='region'
      aria-label={t('Group details')}
      className='bg-muted/25 animate-in fade-in slide-in-from-top-1 border-border/60 border-t px-4 py-4 duration-150'
    >
      <div className='mb-3 flex items-center gap-2'>
        <HugeiconsIcon
          icon={Folder01Icon}
          strokeWidth={2}
          className='text-muted-foreground size-4'
          aria-hidden='true'
        />
        <h3 className='text-sm font-medium'>{t('Group details')}</h3>
        <Badge variant='outline' className='h-5 font-mono font-normal'>
          {groups.length}
        </Badge>
      </div>

      {groups.length > 0 ? (
        <div className='border-border/60 divide-border/50 overflow-hidden rounded-lg border'>
          <div className='bg-muted/40 text-muted-foreground grid grid-cols-[minmax(0,1fr)_7rem] gap-3 px-3 py-2 text-xs font-medium'>
            <span>{t('Group')}</span>
            <span className='text-right'>{t('Multiplier')}</span>
          </div>
          <div className='divide-border/50 divide-y'>
            {groups.map((group) => {
              const metadata = [
                group.id !== group.name ? group.id : '',
                group.description,
              ]
                .filter(Boolean)
                .join(' · ')

              return (
                <div
                  key={group.id}
                  className='grid min-h-12 grid-cols-[minmax(0,1fr)_7rem] items-center gap-3 px-3 py-2'
                >
                  <div className='min-w-0'>
                    <div className='truncate text-sm font-medium'>
                      {group.name}
                    </div>
                    {metadata && (
                      <div className='text-muted-foreground mt-0.5 truncate text-xs'>
                        {metadata}
                      </div>
                    )}
                  </div>
                  <div className='text-right'>
                    <Badge
                      variant='secondary'
                      className='font-mono font-normal tabular-nums'
                    >
                      {formatGroupMultiplier(group)}
                    </Badge>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      ) : (
        <p className='text-muted-foreground py-2 text-sm'>
          {t('No group snapshot available.')}
        </p>
      )}
    </section>
  )
}

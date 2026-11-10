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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import type { SystemTask } from '@/features/system-settings/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import type { ChannelProfitSyncResult } from '../types'

type ProfitSyncTaskDetailProps = {
  task: SystemTask
  result: ChannelProfitSyncResult
}

export function ProfitSyncTaskDetail(props: ProfitSyncTaskDetailProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const result = props.result
  const summary = t(
    '{{upstreams}} upstreams · {{synced}}/{{keys}} keys synced · {{failed}} failed',
    {
      upstreams: formatNumber(result.channels, locale),
      synced: formatNumber(result.synced, locale),
      keys: formatNumber(result.keys, locale),
      failed: formatNumber(result.failed, locale),
    }
  )

  return (
    <div className='space-y-1'>
      <p>{summary}</p>
      <Dialog
        title={t('Profit sync details')}
        description={t(
          "Automatic syncs follow each upstream's interval; different upstreams may become due at different times."
        )}
        trigger={
          <Button variant='link' size='xs' className='h-auto p-0'>
            {t('View sync details')}
          </Button>
        }
        bodyClassName='space-y-4'
      >
        <p className='text-sm'>{summary}</p>
        <dl className='flex flex-wrap gap-x-2 text-sm'>
          <dt className='text-muted-foreground'>{t('Sync trigger')}</dt>
          <dd>
            {props.task.payload?.due_only === true
              ? t('Scheduled')
              : t('Manual or configuration change')}
          </dd>
        </dl>
        {props.task.error ? (
          <p className='text-destructive text-sm wrap-anywhere whitespace-pre-wrap'>
            {props.task.error}
          </p>
        ) : null}
        {result.groups?.length ? (
          result.groups.map((group) => (
            <section
              key={group.channel_ids.join(',')}
              className='space-y-2 rounded-lg border p-3'
            >
              <h3 className='text-sm font-medium wrap-anywhere'>
                {group.name}
              </h3>
              <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm'>
                <dt className='text-muted-foreground'>{t('Channels')}</dt>
                <dd className='wrap-anywhere'>
                  {group.channel_ids.map((id) => `#${id}`).join(', ')}
                </dd>
                <dt className='text-muted-foreground'>{t('Sync interval')}</dt>
                <dd>
                  {t('Every {{minutes}} minutes', {
                    minutes: formatNumber(group.sync_interval_minutes, locale),
                  })}
                </dd>
                <dt className='text-muted-foreground'>
                  {t('Previous sync attempt')}
                </dt>
                <dd>{formatTimestampToDate(group.previous_sync_attempt_at)}</dd>
                <dt className='text-muted-foreground'>
                  {t('This sync attempt')}
                </dt>
                <dd>{formatTimestampToDate(group.attempted_at)}</dd>
                <dt className='text-muted-foreground'>
                  {t('Next eligible sync')}
                </dt>
                <dd>{formatTimestampToDate(group.next_sync_at)}</dd>
              </dl>
              <p className='text-sm'>
                {t('{{synced}}/{{keys}} keys synced · {{failed}} failed', {
                  synced: formatNumber(group.synced, locale),
                  keys: formatNumber(group.keys, locale),
                  failed: formatNumber(group.failed, locale),
                })}
              </p>
              {group.zero_cost ? (
                <p className='text-muted-foreground text-sm'>
                  {t('Zero ratio: no upstream queries')}
                </p>
              ) : null}
              {group.error ? (
                <p className='text-destructive text-sm wrap-anywhere whitespace-pre-wrap'>
                  {group.error}
                </p>
              ) : null}
            </section>
          ))
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('This older task only contains summary counts.')}
          </p>
        )}
      </Dialog>
    </div>
  )
}

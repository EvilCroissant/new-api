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
import { AlertTriangle, Clock3, GitBranch, Network, Upload } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'
import { IconBadge } from '@/components/ui/icon-badge'
import { Label } from '@/components/ui/label'
import { formatUseTime } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { UsageLog } from '../../data/schema'
import { getUpstreamTimingAttempts, parseLogOther } from '../../lib/format'
import type { UpstreamTimingAttempt } from '../../types'

function DetailRow(props: {
  label: React.ReactNode
  value: React.ReactNode
  mono?: boolean
  muted?: boolean
}) {
  return (
    <div className='grid min-w-0 grid-cols-[7.5rem_minmax(0,1fr)] gap-2 text-sm sm:grid-cols-[9.5rem_minmax(0,1fr)] sm:gap-3'>
      <span className='text-muted-foreground min-w-0 text-xs'>
        {props.label}
      </span>
      <span
        className={cn(
          'max-w-full min-w-0 text-xs break-all sm:wrap-break-word',
          props.mono && 'font-mono',
          props.muted && 'text-muted-foreground'
        )}
      >
        {props.value}
      </span>
    </div>
  )
}

function DetailSection(props: {
  icon?: React.ReactNode
  label: string
  variant?: 'default' | 'danger'
  children: React.ReactNode
}) {
  const isDanger = props.variant === 'danger'
  return (
    <div className='min-w-0 space-y-1.5'>
      <Label
        className={cn(
          'flex items-center gap-1.5 text-xs font-semibold',
          isDanger && 'text-red-500'
        )}
      >
        {props.icon && (
          <IconBadge tone={isDanger ? 'destructive' : 'info'} size='xs'>
            {props.icon}
          </IconBadge>
        )}
        {props.label}
      </Label>
      <div
        className={cn(
          'min-w-0 space-y-1 overflow-hidden rounded-md border p-2.5 max-sm:p-2',
          isDanger
            ? 'border-red-200 bg-red-50 dark:border-red-900 dark:bg-red-950/20'
            : 'bg-muted/30'
        )}
      >
        {props.children}
      </div>
    </div>
  )
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function formatDurationMs(value: number | undefined): string {
  if (!isFiniteNumber(value)) return '-'
  if (value < 1000) return `${Math.round(value)}ms`
  return formatUseTime(value / 1000)
}

function formatBytes(value: number | undefined): string {
  if (!isFiniteNumber(value)) return '-'
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(2)} MB`
}

function DurationRow(props: { label: string; value?: number }) {
  if (!isFiniteNumber(props.value)) return null
  return (
    <DetailRow label={props.label} value={formatDurationMs(props.value)} mono />
  )
}

function timingStatusVariant(
  statusCode: number | undefined
): StatusBadgeProps['variant'] {
  if (statusCode == null) return 'neutral'
  return statusCode >= 200 && statusCode < 400 ? 'green' : 'red'
}

function TimingAttempt(props: {
  attempt: UpstreamTimingAttempt
  index: number
}) {
  const { t } = useTranslation()
  const attempt = props.attempt
  const hasConnectionDetails =
    attempt.conn_reused != null ||
    isFiniteNumber(attempt.conn_idle_ms) ||
    isFiniteNumber(attempt.conn_acquire_ms) ||
    isFiniteNumber(attempt.dns_ms) ||
    isFiniteNumber(attempt.dial_ms) ||
    isFiniteNumber(attempt.tls_handshake_ms)
  const hasResponseDetails =
    isFiniteNumber(attempt.upload_ms) ||
    isFiniteNumber(attempt.response_header_wait_ms) ||
    isFiniteNumber(attempt.first_byte_ms) ||
    isFiniteNumber(attempt.header_to_first_sse_ms) ||
    isFiniteNumber(attempt.upstream_first_sse_ms) ||
    isFiniteNumber(attempt.stream_end_ms) ||
    isFiniteNumber(attempt.downstream_first_event_ms) ||
    isFiniteNumber(attempt.downstream_end_ms) ||
    isFiniteNumber(attempt.upstream_to_downstream_first_ms) ||
    isFiniteNumber(attempt.upstream_to_downstream_end_ms)

  return (
    <div
      className={cn(
        'space-y-2',
        props.index > 0 && 'border-border/60 border-t pt-2.5'
      )}
    >
      <div className='flex min-w-0 items-center justify-between gap-2'>
        <div className='flex min-w-0 items-center gap-1.5 text-xs font-semibold'>
          <GitBranch
            className='text-muted-foreground size-3.5'
            aria-hidden='true'
          />
          <span>
            {t('Attempt')} {props.index + 1}
          </span>
          {attempt.retry_index != null && (
            <span className='text-muted-foreground font-mono font-normal'>
              ({t('Retry Index')} {attempt.retry_index})
            </span>
          )}
          {attempt.channel_id != null && (
            <span className='text-muted-foreground font-mono font-normal'>
              #{attempt.channel_id}
            </span>
          )}
        </div>
        {attempt.status_code != null && (
          <StatusBadge
            label={String(attempt.status_code)}
            variant={timingStatusVariant(attempt.status_code)}
            size='sm'
            copyable={false}
          />
        )}
      </div>

      {attempt.body_bytes != null && (
        <DetailRow
          label={t('Request Body')}
          value={formatBytes(attempt.body_bytes)}
          mono
        />
      )}

      {hasConnectionDetails && (
        <div className='space-y-1'>
          <div className='text-muted-foreground flex items-center gap-1 text-[11px] font-medium'>
            <Network className='size-3' aria-hidden='true' />
            {t('Connection Timing')}
          </div>
          {attempt.conn_reused != null && (
            <DetailRow
              label={t('Connection')}
              value={attempt.conn_reused ? t('Reused') : t('New')}
              mono
            />
          )}
          <DurationRow
            label={t('Connection Acquire')}
            value={attempt.conn_acquire_ms}
          />
          <DurationRow label={t('DNS Lookup')} value={attempt.dns_ms} />
          <DurationRow label={t('TCP Dial')} value={attempt.dial_ms} />
          <DurationRow
            label={t('TLS Handshake')}
            value={attempt.tls_handshake_ms}
          />
          {attempt.conn_reused && (
            <DurationRow label={t('Idle Time')} value={attempt.conn_idle_ms} />
          )}
        </div>
      )}

      {hasResponseDetails && (
        <div className='space-y-1'>
          <div className='text-muted-foreground flex items-center gap-1 text-[11px] font-medium'>
            <Upload className='size-3' aria-hidden='true' />
            {t('Request and Response Timing')}
          </div>
          <DurationRow label={t('Request Upload')} value={attempt.upload_ms} />
          <DurationRow
            label={t('Response Header Wait')}
            value={attempt.response_header_wait_ms}
          />
          <DurationRow label={t('First Byte')} value={attempt.first_byte_ms} />
          <DurationRow
            label={t('Header to First SSE')}
            value={attempt.header_to_first_sse_ms}
          />
          <DurationRow
            label={t('Upstream First SSE')}
            value={attempt.upstream_first_sse_ms}
          />
          <DurationRow label={t('Stream End')} value={attempt.stream_end_ms} />
          <DurationRow
            label={t('Downstream First Event')}
            value={attempt.downstream_first_event_ms}
          />
          <DurationRow
            label={t('Downstream End')}
            value={attempt.downstream_end_ms}
          />
          <DurationRow
            label={t('Upstream to Downstream First')}
            value={attempt.upstream_to_downstream_first_ms}
          />
          <DurationRow
            label={t('Upstream to Downstream End')}
            value={attempt.upstream_to_downstream_end_ms}
          />
        </div>
      )}

      {(attempt.write_error || attempt.error) && (
        <div className='space-y-1 rounded border border-red-200 bg-red-50/70 p-2 dark:border-red-900 dark:bg-red-950/20'>
          {attempt.write_error && (
            <DetailRow
              label={t('Write Error')}
              value={attempt.write_error}
              mono
            />
          )}
          {attempt.error && (
            <DetailRow label={t('Request Error')} value={attempt.error} mono />
          )}
        </div>
      )}
    </div>
  )
}

interface TimingDetailsDialogProps {
  log: UsageLog
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function TimingDetailsDialog(props: TimingDetailsDialogProps) {
  const { t } = useTranslation()
  const attempts = getUpstreamTimingAttempts(props.log)
  const other = parseLogOther(props.log.other)

  if (attempts.length === 0) return null

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        <span className='flex items-center gap-2'>
          <Clock3 className='text-info size-4' aria-hidden='true' />
          {t('Timing Details')}
        </span>
      }
      description={t('Timing data recorded for each upstream attempt')}
      contentClassName='min-w-0 overflow-hidden max-sm:max-h-[calc(100dvh-1.5rem)] max-sm:w-[calc(100vw-1.5rem)] max-sm:max-w-[calc(100vw-1.5rem)] max-sm:p-4 sm:max-w-xl'
      headerClassName='max-sm:gap-1'
      titleClassName='flex items-center gap-2 text-base'
      descriptionClassName='sr-only'
      contentHeight='min(72dvh, 720px)'
      bodyClassName='pr-2 sm:pr-4'
    >
      <div className='w-full max-w-full min-w-0 space-y-2.5 overflow-x-hidden py-1 sm:space-y-3'>
        <DetailSection
          icon={<Clock3 className='size-3.5' aria-hidden='true' />}
          label={t('Timing Summary')}
        >
          <DetailRow
            label={t('Response Time')}
            value={formatUseTime(props.log.use_time)}
            mono
          />
          <DetailRow
            label={t('First token')}
            value={formatDurationMs(other?.frt)}
            mono
          />
          <DetailRow
            label={t('Attempts')}
            value={String(attempts.length)}
            mono
          />
        </DetailSection>

        <DetailSection
          icon={<GitBranch className='size-3.5' aria-hidden='true' />}
          label={`${t('Attempts')} (${attempts.length})`}
        >
          {attempts.map((attempt, index) => (
            <TimingAttempt
              key={`${attempt.retry_index ?? 'attempt'}-${attempt.channel_id ?? 'unknown'}-${attempt.upstream_first_sse_ms ?? 'unknown'}-${attempt.status_code ?? 'unknown'}`}
              attempt={attempt}
              index={index}
            />
          ))}
        </DetailSection>

        <div className='text-muted-foreground flex items-start gap-1.5 text-[11px] leading-relaxed'>
          <AlertTriangle
            className='mt-0.5 size-3.5 shrink-0'
            aria-hidden='true'
          />
          <span>
            {t(
              'Timing values are measured from the New API process perspective'
            )}
          </span>
        </div>
      </div>
    </Dialog>
  )
}

import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'

import type { ReferralSummary } from '../types'

type ReferralOverviewProps = {
  summary?: ReferralSummary
  loading: boolean
  referralLink: string
  onTransfer: () => void
}

function formatRewardRate(rate: number): string {
  return `${rate.toLocaleString(undefined, { maximumFractionDigits: 2 })}%`
}

export function ReferralOverview(props: ReferralOverviewProps) {
  const { t } = useTranslation()

  if (props.loading) {
    return (
      <div className='space-y-4'>
        <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-5'>
          {Array.from({ length: 5 }, (_, index) => (
            <Skeleton key={index} className='h-24 rounded-xl' />
          ))}
        </div>
        <Skeleton className='h-40 rounded-xl' />
      </div>
    )
  }

  const summary = props.summary
  if (!summary) return null

  const stats = [
    {
      id: 'invited',
      label: t('Invited Users'),
      value: String(summary.aff_count),
    },
    {
      id: 'rate',
      label: t('Referral Rate'),
      value: formatRewardRate(summary.reward_rate_percent),
    },
    {
      id: 'total',
      label: t('Total Rewards'),
      value: formatQuota(summary.aff_history_quota),
    },
    {
      id: 'available',
      label: t('Available Rewards'),
      value: formatQuota(summary.aff_quota),
    },
    {
      id: 'transferred',
      label: t('Transferred to Balance'),
      value: formatQuota(summary.transferred_quota),
    },
  ]

  return (
    <div className='space-y-4'>
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-5'>
        {stats.map((stat) => (
          <Card key={stat.id} data-card-hover='false' className='gap-0 py-0'>
            <CardContent className='p-4'>
              <p className='text-muted-foreground text-xs font-medium'>
                {stat.label}
              </p>
              <p className='mt-2 truncate text-xl font-semibold tabular-nums'>
                {stat.value}
              </p>
            </CardContent>
          </Card>
        ))}
      </div>

      {!summary.compliance_confirmed ? (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'Referral rewards are currently paused until payment compliance is confirmed.'
            )}
          </AlertDescription>
        </Alert>
      ) : null}
      {summary.compliance_confirmed && summary.reward_rate_bps === 0 ? (
        <Alert>
          <AlertDescription>
            {t(
              'Referral rewards are disabled. The administrator has set the reward rate to 0%.'
            )}
          </AlertDescription>
        </Alert>
      ) : null}

      <Card data-card-hover='false' className='gap-0 py-0'>
        <CardHeader className='border-b p-4 sm:p-5'>
          <CardTitle className='text-base'>{t('Your referral link')}</CardTitle>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Share your invite link. Rewards are issued after an invited user completes an eligible top-up or redeems a code.'
            )}
          </p>
        </CardHeader>
        <CardContent className='flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:p-5'>
          <div className='flex min-w-0 flex-1 items-center gap-2'>
            <Input
              value={props.referralLink}
              readOnly
              className='min-w-0 font-mono text-xs'
              aria-label={t('Your referral link')}
            />
            <CopyButton
              value={props.referralLink}
              variant='outline'
              size='icon'
              className='shrink-0'
              tooltip={t('Copy referral link')}
              aria-label={t('Copy referral link')}
            />
          </div>
          <Button
            type='button'
            className='w-full shrink-0 whitespace-nowrap sm:w-auto'
            onClick={props.onTransfer}
            disabled={summary.aff_quota <= 0 || !summary.compliance_confirmed}
          >
            {t('Transfer to Balance')}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}

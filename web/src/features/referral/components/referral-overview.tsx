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
    [t('Invited Users'), String(summary.aff_count)],
    [t('Referral Rate'), formatRewardRate(summary.reward_rate_percent)],
    [t('Total Rewards'), formatQuota(summary.aff_history_quota)],
    [t('Available Rewards'), formatQuota(summary.aff_quota)],
    [t('Transferred to Balance'), formatQuota(summary.transferred_quota)],
  ]

  return (
    <div className='space-y-4'>
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-5'>
        {stats.map(([label, value]) => (
          <Card key={label} data-card-hover='false' className='gap-0 py-0'>
            <CardContent className='p-4'>
              <p className='text-muted-foreground text-xs font-medium'>
                {label}
              </p>
              <p className='mt-2 truncate text-xl font-semibold tabular-nums'>
                {value}
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
        <CardContent className='grid gap-3 p-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end sm:p-5'>
          <div className='space-y-2'>
            <p className='text-muted-foreground text-xs font-medium'>
              {t('Invitation Code')}
            </p>
            <div className='flex gap-2'>
              <Input value={summary.aff_code} readOnly className='font-mono' />
              <CopyButton
                value={summary.aff_code}
                variant='outline'
                className='shrink-0'
                tooltip={t('Copy invitation code')}
                aria-label={t('Copy invitation code')}
              />
            </div>
          </div>
          <div className='flex gap-2 sm:justify-end'>
            <CopyButton
              value={props.referralLink}
              variant='outline'
              className='flex-1 sm:flex-none'
              tooltip={t('Copy referral link')}
              aria-label={t('Copy referral link')}
            >
              {t('Copy referral link')}
            </CopyButton>
            <Button
              type='button'
              className='flex-1 sm:flex-none'
              onClick={props.onTransfer}
              disabled={summary.aff_quota <= 0 || !summary.compliance_confirmed}
            >
              {t('Transfer to Balance')}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

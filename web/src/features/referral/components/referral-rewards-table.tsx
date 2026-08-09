import { Gift } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatQuota, formatTimestamp } from '@/lib/format'

import type { ReferralReward, ReferralRewardPage } from '../types'

type ReferralRewardsTableProps = {
  page?: ReferralRewardPage
  loading: boolean
  onPageChange: (page: number) => void
}

const LOADING_ROW_IDS = [
  'referral-reward-loading-1',
  'referral-reward-loading-2',
  'referral-reward-loading-3',
  'referral-reward-loading-4',
] as const

function formatRate(rateBps: number): string {
  return `${(rateBps / 100).toLocaleString(undefined, {
    maximumFractionDigits: 2,
  })}%`
}

function getSourceLabel(reward: ReferralReward, t: (key: string) => string) {
  return reward.source_type === 'redemption'
    ? t('Redemption Code')
    : t('Online Top-Up')
}

export function ReferralRewardsTable(props: ReferralRewardsTableProps) {
  const { t } = useTranslation()
  const rewards = props.page?.items ?? []
  const currentPage = props.page?.page ?? 1
  const pageSize = props.page?.page_size ?? 20
  const total = props.page?.total ?? 0
  const hasPrevious = currentPage > 1
  const hasNext = currentPage * pageSize < total

  if (props.loading) {
    return (
      <div className='space-y-2 py-2'>
        {LOADING_ROW_IDS.map((rowId) => (
          <Skeleton key={rowId} className='h-12 w-full rounded-lg' />
        ))}
      </div>
    )
  }

  if (rewards.length === 0) {
    return (
      <div className='flex flex-col items-center justify-center py-12 text-center'>
        <div className='bg-muted/50 text-muted-foreground/60 mb-3 rounded-full p-3.5'>
          <Gift className='h-6 w-6' aria-hidden='true' />
        </div>
        <p className='text-muted-foreground text-sm font-medium'>
          {t('No referral rewards yet')}
        </p>
        <p className='text-muted-foreground/60 mt-1 max-w-sm text-balance text-xs'>
          {t(
            'Share your invite link. Rewards will automatically appear here after invited users top up.'
          )}
        </p>
      </div>
    )
  }

  return (
    <div className='space-y-3'>
      <Table className='min-w-[720px] table-fixed'>
        <TableHeader>
          <TableRow>
            <TableHead className='w-[22%]'>{t('Time')}</TableHead>
            <TableHead className='w-[16%]'>{t('Type')}</TableHead>
            <TableHead className='w-[18%] text-right'>
              {t('Recharge Amount')}
            </TableHead>
            <TableHead className='w-[14%] text-right'>
              {t('Rate')}
            </TableHead>
            <TableHead className='w-[15%] text-right'>
              {t('Commission')}
            </TableHead>
            <TableHead className='w-[15%] text-center'>
              {t('Status')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rewards.map((reward) => (
            <TableRow key={reward.id}>
              <TableCell className='text-muted-foreground truncate font-mono text-xs'>
                {formatTimestamp(reward.issued_at)}
              </TableCell>
              <TableCell className='truncate text-xs'>
                {getSourceLabel(reward, t)}
              </TableCell>
              <TableCell className='truncate text-right font-mono text-xs tabular-nums'>
                {formatQuota(reward.base_quota)}
              </TableCell>
              <TableCell className='text-muted-foreground truncate text-right font-mono text-xs tabular-nums'>
                {formatRate(reward.reward_rate_bps)}
              </TableCell>
              <TableCell className='truncate text-right font-mono text-xs tabular-nums'>
                +{formatQuota(reward.reward_quota)}
              </TableCell>
              <TableCell className='text-center'>
                <Badge
                  variant={reward.status === 'issued' ? 'outline' : 'warning'}
                  className={
                    reward.status === 'issued'
                      ? 'border-success/40 bg-success/10 text-success'
                      : undefined
                  }
                >
                  {reward.status === 'issued' ? t('Issued') : t('Reversed')}
                </Badge>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {total > pageSize ? (
        <div className='flex items-center justify-end gap-2 pt-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => props.onPageChange(currentPage - 1)}
            disabled={!hasPrevious || props.loading}
          >
            {t('Previous')}
          </Button>
          <span className='text-muted-foreground text-xs tabular-nums'>
            {currentPage} / {Math.ceil(total / pageSize)}
          </span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => props.onPageChange(currentPage + 1)}
            disabled={!hasNext || props.loading}
          >
            {t('Next')}
          </Button>
        </div>
      ) : null}
    </div>
  )
}

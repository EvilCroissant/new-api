import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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

  return (
    <div className='space-y-3'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Time')}</TableHead>
            <TableHead>{t('Source')}</TableHead>
            <TableHead className='text-right'>{t('Qualified Credit')}</TableHead>
            <TableHead className='text-right'>{t('Reward Rate')}</TableHead>
            <TableHead className='text-right'>{t('Reward')}</TableHead>
            <TableHead className='text-right'>{t('Status')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.loading ? (
            <TableRow>
              <TableCell colSpan={6} className='text-muted-foreground h-28 text-center'>
                {t('Loading...')}
              </TableCell>
            </TableRow>
          ) : null}
          {!props.loading && rewards.length === 0 ? (
            <TableRow>
              <TableCell colSpan={6} className='text-muted-foreground h-28 text-center'>
                {t('No referral rewards yet')}
              </TableCell>
            </TableRow>
          ) : null}
          {!props.loading
            ? rewards.map((reward) => (
                <TableRow key={reward.id}>
                  <TableCell className='text-muted-foreground text-xs'>
                    {formatTimestamp(reward.issued_at)}
                  </TableCell>
                  <TableCell>{getSourceLabel(reward, t)}</TableCell>
                  <TableCell className='text-right'>
                    {formatQuota(reward.base_quota)}
                  </TableCell>
                  <TableCell className='text-right'>
                    {formatRate(reward.reward_rate_bps)}
                  </TableCell>
                  <TableCell className='text-right font-medium'>
                    {formatQuota(reward.reward_quota)}
                  </TableCell>
                  <TableCell className='text-right'>
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
              ))
            : null}
        </TableBody>
      </Table>
      {total > pageSize ? (
        <div className='flex items-center justify-end gap-2'>
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

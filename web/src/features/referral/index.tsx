import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { TransferDialog } from '@/features/wallet/components/dialogs/transfer-dialog'

import {
  getReferralRewards,
  getReferralSummary,
  transferReferralRewards,
} from './api'
import { ReferralOverview } from './components/referral-overview'
import { ReferralRewardsTable } from './components/referral-rewards-table'
import { generateReferralLink } from './lib'

const REWARDS_PAGE_SIZE = 20

function getErrorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback
}

export function Referral() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [transferOpen, setTransferOpen] = useState(false)

  const summaryQuery = useQuery({
    queryKey: ['referral-summary'],
    queryFn: async () => {
      const response = await getReferralSummary()
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load referral program'))
      }
      return response.data
    },
  })

  const rewardsQuery = useQuery({
    queryKey: ['referral-rewards', page],
    queryFn: async () => {
      const response = await getReferralRewards(page, REWARDS_PAGE_SIZE)
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load referral rewards'))
      }
      return response.data
    },
  })

  const transferMutation = useMutation({
    mutationFn: async (quota: number) => {
      const response = await transferReferralRewards(quota)
      if (!response.success) {
        throw new Error(response.message || t('Transfer failed'))
      }
    },
    onSuccess: async () => {
      toast.success(t('Transfer successful'))
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['referral-summary'] }),
        queryClient.invalidateQueries({ queryKey: ['referral-rewards'] }),
      ])
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Transfer failed')))
    },
  })

  const handleTransfer = async (quota: number): Promise<boolean> => {
    try {
      await transferMutation.mutateAsync(quota)
      return true
    } catch {
      return false
    }
  }

  const referralLink = generateReferralLink(summaryQuery.data?.aff_code ?? '')

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Referral Program')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
            <ReferralOverview
              summary={summaryQuery.data}
              loading={summaryQuery.isLoading}
              referralLink={referralLink}
              onTransfer={() => setTransferOpen(true)}
            />

            <Card data-card-hover='false' className='gap-0 py-0'>
              <CardHeader className='border-b p-4 sm:p-5'>
                <CardTitle className='text-base'>{t('Reward Activity')}</CardTitle>
              </CardHeader>
              <CardContent className='p-3 sm:p-5'>
                <ReferralRewardsTable
                  page={rewardsQuery.data}
                  loading={rewardsQuery.isLoading}
                  onPageChange={setPage}
                />
              </CardContent>
            </Card>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <TransferDialog
        open={transferOpen}
        onOpenChange={setTransferOpen}
        onConfirm={handleTransfer}
        availableQuota={summaryQuery.data?.aff_quota ?? 0}
        transferring={transferMutation.isPending}
      />
    </>
  )
}

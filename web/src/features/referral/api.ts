import { api, type ApiRequestConfig } from '@/lib/api'

import type {
  ReferralApiResponse,
  ReferralRewardPage,
  ReferralSummary,
} from './types'

const referralActionConfig = {
  skipBusinessError: true,
  skipErrorHandler: true,
} satisfies ApiRequestConfig

export async function getReferralSummary(): Promise<
  ReferralApiResponse<ReferralSummary>
> {
  const response = await api.get('/api/user/aff')
  return response.data
}

export async function getReferralRewards(
  page: number,
  pageSize: number
): Promise<ReferralApiResponse<ReferralRewardPage>> {
  const response = await api.get('/api/user/aff/rewards', {
    params: { p: page, page_size: pageSize },
  })
  return response.data
}

export async function transferReferralRewards(
  quota: number
): Promise<ReferralApiResponse> {
  const response = await api.post(
    '/api/user/aff_transfer',
    { quota },
    referralActionConfig
  )
  return response.data
}

export type ReferralApiResponse<T = unknown> = {
  success: boolean
  message?: string
  data?: T
}

export type ReferralSummary = {
  aff_code: string
  aff_count: number
  aff_quota: number
  aff_history_quota: number
  transferred_quota: number
  reward_rate_bps: number
  reward_rate_percent: number
  compliance_confirmed: boolean
}

export type ReferralReward = {
  id: number
  source_type: 'topup' | 'redemption'
  base_quota: number
  reward_rate_bps: number
  reward_quota: number
  status: 'issued' | 'reversed'
  issued_at: number
}

export type ReferralRewardPage = {
  page: number
  page_size: number
  total: number
  items: ReferralReward[]
}

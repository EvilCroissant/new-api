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

export type ChannelProfitStatus =
  | 'disabled'
  | 'pending'
  | 'synced'
  | 'partial'
  | 'error'

export type ChannelProfitProvider = '' | 'new_api' | 'sub2api' | 'mixed'

export type ChannelProfitGroupRatio = {
  group: string
  ratio: number
}

export type ChannelProfitKey = {
  key_id: string
  key_hint: string
  key_name: string
  channel_ids: number[]
  channel_names: string[]
  provider: ChannelProfitProvider
  upstream_group: string
  upstream_group_ratio: number
  ratio_available: boolean
  cost_usd: number
  cost_available: boolean
  partial: boolean
  last_synced_at: number
  last_error: string
  upstream_quota_per_unit: number
  upstream_consumed_quota: number
}

export type ChannelProfitRow = {
  group_id: string
  channel_id: number
  channel_ids: number[]
  channel_name: string
  base_url: string
  provider: ChannelProfitProvider
  enabled: boolean
  sync_interval_minutes: number
  last_sync_attempt_at: number
  access_token_configured: boolean
  revenue_usd: number
  cost_usd: number
  cost_available: boolean
  profit_usd: number
  profit_available: boolean
  margin: number
  margin_available: boolean
  partial: boolean
  status: ChannelProfitStatus
  last_synced_at: number
  last_error: string
  downstream_rates: ChannelProfitGroupRatio[]
  keys: ChannelProfitKey[]
}

export type ChannelProfitConfigInput = {
  enabled?: boolean
  display_name?: string
  sync_interval_minutes?: number
  access_token?: string
}

export type ChannelProfitSummary = {
  usage_date: string
  revenue_usd: number
  cost_usd: number
  cost_available: boolean
  profit_usd: number
  profit_available: boolean
  margin: number
  margin_available: boolean
  partial: boolean
  last_synced_at: number
  rows: ChannelProfitRow[]
}

export type ChannelProfitResponse = {
  success: boolean
  message?: string
  data?: ChannelProfitSummary
}

export type ChannelProfitConfigResponse = {
  success: boolean
  message?: string
}

export type ChannelProfitSyncResponse = {
  success: boolean
  message?: string
  data?: {
    created: boolean
    task: {
      task_id: string
      status: string
    }
  }
}

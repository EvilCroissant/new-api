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

export type UpstreamMonitorProvider = 'newapi' | 'sub2api'

export type UpstreamMonitor = {
  id: number
  name: string
  base_url: string
  provider: UpstreamMonitorProvider
  new_api_user_id: number
  access_token_configured: boolean
  refresh_token_configured: boolean
  balance_usd: number
  balance_available: boolean
  group_count: number
  pricing_count: number
  groups?: unknown
  pricing?: unknown
  last_synced_at: number
  last_error: string
  created_at: number
  updated_at: number
}

export type UpstreamMonitorDetectResult = {
  base_url: string
  provider?: UpstreamMonitorProvider
  detected: boolean
}

export type UpstreamMonitorCreateInput = {
  name?: string
  base_url: string
  provider: UpstreamMonitorProvider
  new_api_user_id?: number
  access_token: string
  refresh_token?: string
}

export type UpstreamMonitorUpdateInput = {
  new_api_user_id?: number
  access_token?: string
  refresh_token?: string
}

export type UpstreamMonitorResponse<T> = {
  success: boolean
  message?: string
  data?: T
}

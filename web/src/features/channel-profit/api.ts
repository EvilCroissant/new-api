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

import { api } from '@/lib/api'

import type {
  ChannelProfitConfigResponse,
  ChannelProfitResponse,
  ChannelProfitSyncResponse,
} from './types'

export async function getChannelProfit(date: string) {
  const res = await api.get<ChannelProfitResponse>('/api/channel-profit/', {
    params: { date },
  })
  return res.data
}

export async function updateChannelProfitMonitoring(
  channelId: number,
  enabled: boolean
) {
  const res = await api.put<ChannelProfitConfigResponse>(
    `/api/channel-profit/${channelId}`,
    { enabled },
    { skipErrorHandler: true }
  )
  return res.data
}

export async function syncChannelProfit() {
  const res = await api.post<ChannelProfitSyncResponse>(
    '/api/channel-profit/sync',
    undefined,
    { skipErrorHandler: true }
  )
  return res.data
}

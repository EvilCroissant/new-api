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
  UpstreamMonitor,
  UpstreamMonitorCreateInput,
  UpstreamMonitorDetectResult,
  UpstreamMonitorResponse,
  UpstreamMonitorUpdateInput,
} from './types'

export async function listUpstreamMonitors() {
  const response = await api.get<UpstreamMonitorResponse<UpstreamMonitor[]>>(
    '/api/upstream-monitors/'
  )
  return response.data
}

export async function detectUpstreamMonitor(baseURL: string) {
  const response = await api.post<
    UpstreamMonitorResponse<UpstreamMonitorDetectResult>
  >(
    '/api/upstream-monitors/detect',
    { base_url: baseURL },
    { skipErrorHandler: true }
  )
  return response.data
}

export async function createUpstreamMonitor(input: UpstreamMonitorCreateInput) {
  const response = await api.post<UpstreamMonitorResponse<UpstreamMonitor>>(
    '/api/upstream-monitors/',
    input,
    { skipErrorHandler: true }
  )
  return response.data
}

export async function updateUpstreamMonitor(
  id: number,
  input: UpstreamMonitorUpdateInput
) {
  const response = await api.put<UpstreamMonitorResponse<UpstreamMonitor>>(
    `/api/upstream-monitors/${id}`,
    input,
    { skipErrorHandler: true }
  )
  return response.data
}

export async function syncUpstreamMonitor(id: number) {
  const response = await api.post<UpstreamMonitorResponse<UpstreamMonitor>>(
    `/api/upstream-monitors/${id}/sync`,
    undefined,
    { skipErrorHandler: true }
  )
  return response.data
}

export async function deleteUpstreamMonitor(id: number) {
  const response = await api.delete<UpstreamMonitorResponse<null>>(
    `/api/upstream-monitors/${id}`,
    { skipErrorHandler: true }
  )
  return response.data
}

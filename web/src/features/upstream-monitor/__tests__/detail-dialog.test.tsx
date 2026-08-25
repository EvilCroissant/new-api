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
*/

import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, test } from 'vitest'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { UpstreamMonitorDetailDialog } =
  await import('../components/upstream-monitor-detail-dialog')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

type ApiMethod = (url: string) => Promise<{ data: unknown }>
type MockableApi = { get: ApiMethod }

const apiClient = api as unknown as MockableApi
const originalGet = apiClient.get
const queryClients: InstanceType<typeof QueryClient>[] = []

function renderDialog(groups: unknown): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  apiClient.get = async (url) => {
    expect(url).toBe('/api/upstream-monitors/1')
    return {
      data: {
        success: true,
        data: {
          id: 1,
          base_url: 'https://monitor.example.com',
          groups,
        },
      },
    }
  }

  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <UpstreamMonitorDetailDialog
          monitorId={1}
          open
          onOpenChange={() => undefined}
        />
      </I18nextProvider>
    </QueryClientProvider>
  )
}

afterEach(() => {
  apiClient.get = originalGet
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
})

describe('UpstreamMonitorDetailDialog', () => {
  test('shows every available group with the current user multiplier', async () => {
    renderDialog({
      groups: [
        {
          id: 1,
          name: 'ChatGPT-Plus 【稳定通道】',
          description: 'Plus group',
          rate_multiplier: 0.1,
        },
        {
          id: 2,
          name: 'ChatGPT-Pro 【高并发通道】',
          description: 'Pro group',
          rate_multiplier: 0.2,
        },
      ],
      rates: { '1': 0.045 },
    })

    expect(
      await screen.findByRole('columnheader', { name: 'Group' })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('columnheader', { name: 'Description' })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('columnheader', { name: 'Multiplier' })
    ).toBeInTheDocument()
    expect(screen.getByText('ChatGPT-Plus 【稳定通道】')).toBeInTheDocument()
    expect(screen.getByText('Plus group')).toBeInTheDocument()
    expect(screen.getByText('0.045x')).toBeInTheDocument()
    expect(screen.getByText('ChatGPT-Pro 【高并发通道】')).toBeInTheDocument()
    expect(screen.getByText('Pro group')).toBeInTheDocument()
    expect(screen.getByText('0.2x')).toBeInTheDocument()
    expect(screen.queryByText(/"groups"/)).not.toBeInTheDocument()
  })

  test('shows New API group descriptions and multipliers', async () => {
    renderDialog({
      success: true,
      data: {
        'gpt-pro': {
          desc: 'GPT-Pro 小队',
          ratio: 0.2,
        },
        auto: {
          desc: '自动路由',
          ratio: '自动',
        },
      },
    })

    expect(await screen.findByText('gpt-pro')).toBeInTheDocument()
    expect(screen.getByText('GPT-Pro 小队')).toBeInTheDocument()
    expect(screen.getByText('0.2x')).toBeInTheDocument()
    expect(screen.getByText('auto')).toBeInTheDocument()
    expect(screen.getByText('自动路由')).toBeInTheDocument()
    expect(screen.getByText('自动')).toBeInTheDocument()
  })
})

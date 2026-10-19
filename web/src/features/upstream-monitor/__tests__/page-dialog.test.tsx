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

import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, test } from 'vitest'

import type { UpstreamMonitor } from '../types'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { ROLE } = await import('@/lib/roles')
const { useAuthStore } = await import('@/stores/auth-store')
const { UpstreamMonitorPage } = await import('..')
const { UpstreamMonitorTable } =
  await import('../components/upstream-monitor-table')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

type ApiMethod = (url: string) => Promise<{ data: unknown }>
type MockableApi = { get: ApiMethod; post: ApiMethod }

const apiClient = api as unknown as MockableApi
const originalGet = apiClient.get
const originalPost = apiClient.post
const queryClients: InstanceType<typeof QueryClient>[] = []

function renderPage(monitors: UpstreamMonitor[] = []): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'root',
    role: ROLE.SUPER_ADMIN,
  })
  apiClient.get = async () => ({
    data: { success: true, data: monitors },
  })

  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <UpstreamMonitorPage />
      </I18nextProvider>
    </QueryClientProvider>
  )
}

afterEach(() => {
  apiClient.get = originalGet
  apiClient.post = originalPost
  useAuthStore.getState().auth.setUser(null)
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
})

describe('UpstreamMonitorPage', () => {
  const monitor: UpstreamMonitor = {
    id: 1,
    name: 'Upstream',
    base_url: 'https://monitor.example.com',
    provider: 'sub2api',
    new_api_user_id: 0,
    api_key_configured: true,
    access_token_configured: true,
    refresh_token_configured: false,
    balance_usd: 1,
    balance_available: true,
    group_count: 0,
    pricing_count: 0,
    last_synced_at: 1,
    last_error: '',
    created_at: 1,
    updated_at: 1,
  }

  test('uses amber for low wallet balance and red for query failure without stale low-balance warnings', async () => {
    renderPage([
      monitor,
      {
        ...monitor,
        id: 2,
        name: 'Failed upstream',
        last_error: 'Wallet lookup failed',
      },
    ])
    const warning = await screen.findByText('Low balance')
    expect(warning).toHaveClass('text-warning')
    expect(await screen.findByText('Sync failed')).toHaveClass(
      'text-destructive'
    )
    expect(screen.getAllByLabelText('Low balance')).toHaveLength(1)
  })

  test('reloads the failed synchronization status immediately after a failed manual query', async () => {
    renderPage([monitor])
    await screen.findByText('Upstream')
    apiClient.post = async () => ({
      data: { success: false, message: 'Wallet lookup failed' },
    })
    apiClient.get = async () => ({
      data: {
        success: true,
        data: [{ ...monitor, last_error: 'Wallet lookup failed' }],
      },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Sync now' }))
    expect(await screen.findByText('Sync failed')).toHaveClass(
      'text-destructive'
    )
    expect(screen.queryByLabelText('Low balance')).not.toBeInTheDocument()
  })

  test('shows a red unavailable balance instead of the stale wallet in the embedded channel table', () => {
    render(
      <I18nextProvider i18n={i18n}>
        <UpstreamMonitorTable
          embedded
          monitors={[{ ...monitor, last_error: 'Wallet lookup failed' }]}
          isRoot
          syncingId={undefined}
          deletingId={undefined}
          updatingId={undefined}
          onSync={() => undefined}
          onDelete={() => undefined}
          onUpdateCredentials={async () => undefined}
        />
      </I18nextProvider>
    )
    expect(screen.getByLabelText('Available balance')).toHaveTextContent('-')
    expect(screen.getByLabelText('Available balance')).toHaveClass(
      'text-destructive'
    )
    expect(screen.getByText('Sync failed')).toHaveClass('text-destructive')
    expect(screen.queryByLabelText('Low balance')).not.toBeInTheDocument()
  })

  test('keeps New API balance display and synchronization status unchanged', async () => {
    renderPage([{ ...monitor, provider: 'newapi', new_api_user_id: 1 }])
    expect(await screen.findByText('Synchronized')).toBeInTheDocument()
    expect(screen.getByLabelText('Low balance')).toBeInTheDocument()
    expect(screen.queryByText('Low balance')).not.toBeInTheDocument()
  })

  test('opens the add monitor dialog when a super administrator clicks Add monitor', async () => {
    renderPage()

    await screen.findByText('No upstream monitors')
    fireEvent.click(screen.getByRole('button', { name: 'Add monitor' }))

    expect(
      await screen.findByRole('dialog', { name: 'Add upstream monitor' })
    ).toBeInTheDocument()
  })
})

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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ChannelProfitRow } from '@/features/channel-profit/types'
import type { UpstreamMonitor } from '@/features/upstream-monitor/types'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { Route as ProfitRoute } from '@/routes/_authenticated/profit'
import { Route as ManagementRoute } from '@/routes/_authenticated/upstream-monitor'
import { useAuthStore } from '@/stores/auth-store'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const queryClients: QueryClient[] = []
let requestedURLs: string[] = []
let profitRows: ChannelProfitRow[] = []
let accountMonitors: UpstreamMonitor[] = []
let profitLoadError = false

const row: ChannelProfitRow = {
  group_id: 'test-site',
  channel_id: 182,
  channel_ids: [182],
  channel_names: ['Test upstream'],
  channel_name: 'Test upstream',
  base_url: 'https://example.com/v1/',
  provider: 'new_api',
  enabled: true,
  cost_mode: 'ratio',
  cost_factor: 1,
  manual_ratio: null,
  request_cost_usd: null,
  sync_interval_minutes: 60,
  access_token_configured: false,
  last_sync_attempt_at: 0,
  last_synced_at: 0,
  last_error: '',
  cost_sync_error: '',
  revenue_usd: 0,
  cost_usd: 0,
  cost_available: false,
  profit_usd: 0,
  profit_available: false,
  margin: 0,
  margin_available: false,
  partial: false,
  status: 'pending',
  downstream_rates: [],
  request_coverage: {
    total: 0,
    matched: 0,
    estimated: 0,
    unknown: 0,
    revenue_usd: 0,
    cost_usd: 0,
  },
  keys: [],
}

const monitor = {
  id: 9,
  name: 'Test account',
  base_url: 'https://example.com',
  provider: 'newapi',
  new_api_user_id: 42,
  access_token_configured: true,
  refresh_token_configured: false,
  balance_available: true,
  balance_usd: 12.34,
  last_error: '',
  last_synced_at: 0,
} as UpstreamMonitor

beforeEach(() => {
  requestedURLs = []
  profitRows = []
  accountMonitors = []
  profitLoadError = false
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'root',
    role: ROLE.SUPER_ADMIN,
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    requestedURLs.push(url)
    if (url === '/api/upstream-monitors/') {
      return { data: { success: true, data: accountMonitors } }
    }
    if (url === '/api/channel-profit/') {
      if (profitLoadError) {
        return { data: { success: false, message: 'Profit unavailable' } }
      }
      return {
        data: {
          success: true,
          data: {
            usage_date: '2026-10-10',
            revenue_usd: 12,
            cost_usd: 8,
            cost_available: true,
            profit_usd: 4,
            profit_available: true,
            margin: 1 / 3,
            margin_available: true,
            partial: false,
            last_synced_at: 0,
            rows: profitRows,
          },
        },
      }
    }
    return { data: { success: true, data: {} } }
  })
})

afterEach(() => {
  for (const client of queryClients) client.clear()
  queryClients.length = 0
  useAuthStore.getState().auth.reset()
})

async function renderPage(path = '/upstream-monitor') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(client)
  const root = createRootRoute()
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
  })
  const management = createRoute({
    getParentRoute: () => authenticated,
    path: 'upstream-monitor/',
    component: ManagementRoute.options.component,
    validateSearch: ManagementRoute.options.validateSearch,
    beforeLoad: ManagementRoute.options.beforeLoad as () => void,
  })
  const profit = createRoute({
    getParentRoute: () => authenticated,
    path: 'profit/',
    beforeLoad: ProfitRoute.options.beforeLoad as () => void,
  })
  const forbidden = createRoute({
    getParentRoute: () => root,
    path: '403',
    component: () => <p>Forbidden</p>,
  })
  const router = createRouter({
    routeTree: root.addChildren([
      authenticated.addChildren([management, profit]),
      forbidden,
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nextProvider>
    </QueryClientProvider>
  )
  return router
}

describe('upstream management navigation', () => {
  it('saves existing account credentials and profit settings through one dialog', async () => {
    profitRows = [row]
    accountMonitors = [monitor]
    const update = vi.spyOn(api, 'put').mockImplementation(async (url) => ({
      data: {
        success: true,
        data: url === '/api/upstream-monitors/9' ? monitor : {},
      },
    }))
    await renderPage()
    await screen.findByLabelText('Available balance')
    expect(
      screen.queryByRole('button', { name: 'View groups' })
    ).not.toBeInTheDocument()
    expect(screen.queryByText('Test account')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    fireEvent.change(screen.getByLabelText('Personal access token'), {
      target: { value: 'replacement' },
    })
    fireEvent.change(screen.getByLabelText('Purchase cost factor'), {
      target: { value: '0.8' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(update).toHaveBeenCalledWith(
      '/api/upstream-monitors/9',
      { new_api_user_id: 42, access_token: 'replacement' },
      expect.anything()
    )
    expect(update).toHaveBeenCalledWith(
      '/api/channel-profit/182',
      expect.objectContaining({ cost_factor: 0.8 }),
      expect.anything()
    )
  })

  it('reuses the account after pricing save fails and keeps the entered form for retry', async () => {
    profitRows = [row]
    const create = vi.spyOn(api, 'post').mockImplementation(async () => {
      accountMonitors = [monitor]
      return { data: { success: true, data: monitor } }
    })
    let pricingAttempts = 0
    const update = vi.spyOn(api, 'put').mockImplementation(async (url) => {
      if (url === '/api/channel-profit/182' && ++pricingAttempts === 1) {
        return { data: { success: false, message: 'Pricing save failed' } }
      }
      return {
        data: {
          success: true,
          data: url === '/api/upstream-monitors/9' ? monitor : {},
        },
      }
    })
    await renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Settings' }))
    fireEvent.change(screen.getByLabelText('Site type'), {
      target: { value: 'newapi' },
    })
    fireEvent.change(screen.getByLabelText('New API user ID'), {
      target: { value: '42' },
    })
    fireEvent.change(screen.getByLabelText('Personal access token'), {
      target: { value: 'replacement' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(pricingAttempts).toBe(1))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
    )
    expect(screen.getByRole('dialog')).toBeVisible()
    expect(screen.getByLabelText('Personal access token')).toHaveValue(
      'replacement'
    )
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(create).toHaveBeenCalledTimes(1)
    expect(update).toHaveBeenCalledWith(
      '/api/upstream-monitors/9',
      expect.objectContaining({ access_token: 'replacement' }),
      expect.anything()
    )
    expect(pricingAttempts).toBe(2)
  })

  it('retains standalone account monitors when profit loading fails and removes group browsing', async () => {
    accountMonitors = [monitor]
    profitLoadError = true
    await renderPage()
    expect(await screen.findByText('Test account')).toBeVisible()
    expect(screen.getByText('Profit unavailable')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'View groups' })
    ).not.toBeInTheDocument()
  })
  it.each([
    '/upstream-monitor',
    '/upstream-monitor?tab=accounts',
    '/upstream-monitor?tab=invalid',
  ])('shows one profit screen with account data at %s', async (path) => {
    await renderPage(path)
    await screen.findByText('Downstream revenue')
    expect(
      screen.getByRole('heading', { name: 'Upstream management' })
    ).toBeVisible()
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
    expect(requestedURLs).toContain('/api/channel-profit/')
    expect(requestedURLs).toContain('/api/upstream-monitors/')
    fireEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    expect(
      await screen.findByRole('dialog', { name: 'Add upstream monitor' })
    ).toBeVisible()
  })

  it('opens the unified screen from the legacy profit link', async () => {
    const router = await renderPage('/profit')
    await screen.findByText('Downstream revenue')
    expect(router.state.location.pathname).toBe('/upstream-monitor')
    expect(requestedURLs).toContain('/api/upstream-monitors/')
  })

  it('keeps ordinary administrators read-only', async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 2, username: 'admin', role: ROLE.ADMIN })
    await renderPage()
    await screen.findByText('Downstream revenue')
    expect(
      screen.queryByRole('button', { name: 'Add monitor' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Sync settings' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Sync now' })
    ).not.toBeInTheDocument()
  })

  it.each(['/profit', '/upstream-monitor?tab=profit', '/upstream-monitor'])(
    'denies non-administrators at %s before querying upstream data',
    async (path) => {
      useAuthStore
        .getState()
        .auth.setUser({ id: 3, username: 'user', role: ROLE.USER })
      const router = await renderPage(path)
      await waitFor(() => expect(router.state.location.pathname).toBe('/403'))
      expect(await screen.findByText('Forbidden')).toBeInTheDocument()
      expect(requestedURLs).toEqual([])
    }
  )
})

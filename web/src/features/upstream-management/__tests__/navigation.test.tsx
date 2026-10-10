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
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

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

beforeEach(() => {
  requestedURLs = []
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'root',
    role: ROLE.SUPER_ADMIN,
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    requestedURLs.push(url)
    if (url === '/api/upstream-monitors/') {
      return { data: { success: true, data: [] } }
    }
    if (url === '/api/channel-profit/') {
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
            rows: [],
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
  it('switches views and restores the selected tab through browser history', async () => {
    const router = await renderPage()
    await screen.findByText('No upstream monitors')
    expect(
      screen.getByRole('heading', { name: 'Upstream management' })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('tab', { name: 'Account monitoring' })
    ).toHaveAttribute('aria-selected', 'true')
    expect(requestedURLs).not.toContain('/api/channel-profit/')

    fireEvent.click(screen.getByRole('tab', { name: 'Channel profit' }))
    await screen.findByText('Downstream revenue')
    expect(
      screen.getByRole('tabpanel', { name: 'Channel profit' })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Add monitor' })
    ).not.toBeInTheDocument()
    expect(router.state.location.search).toEqual({ tab: 'profit' })

    await act(async () => router.history.back())
    await screen.findByText('No upstream monitors')
    expect(
      screen.getByRole('tab', { name: 'Account monitoring' })
    ).toHaveAttribute('aria-selected', 'true')
    fireEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    expect(
      await screen.findByRole('dialog', { name: 'Add upstream monitor' })
    ).toBeInTheDocument()
  })

  it('opens the profit tab from the legacy profit link without loading account data', async () => {
    const router = await renderPage('/profit')
    await screen.findByText('Downstream revenue')
    expect(router.state.location.pathname).toBe('/upstream-monitor')
    expect(router.state.location.search).toEqual({ tab: 'profit' })
    expect(requestedURLs).not.toContain('/api/upstream-monitors/')
  })

  it('falls back to accounts for an invalid tab and supports keyboard tab switching', async () => {
    const user = userEvent.setup()
    await renderPage('/upstream-monitor?tab=invalid')
    await screen.findByText('No upstream monitors')
    screen.getByRole('tab', { name: 'Account monitoring' }).focus()
    await user.keyboard('{ArrowRight}{Enter}')
    await screen.findByText('Downstream revenue')
    expect(screen.getByRole('tab', { name: 'Channel profit' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
  })

  it('keeps ordinary administrators read-only in both views', async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 2, username: 'admin', role: ROLE.ADMIN })
    await renderPage()
    await screen.findByText('No upstream monitors')
    expect(
      screen.queryByRole('button', { name: 'Add monitor' })
    ).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: 'Channel profit' }))
    await screen.findByText('Downstream revenue')
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

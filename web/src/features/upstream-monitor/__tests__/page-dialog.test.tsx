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

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { ROLE } = await import('@/lib/roles')
const { useAuthStore } = await import('@/stores/auth-store')
const { UpstreamMonitorPage } = await import('..')

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

function renderPage(): void {
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
    data: { success: true, data: [] },
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
  useAuthStore.getState().auth.setUser(null)
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
})

describe('UpstreamMonitorPage', () => {
  test('opens the add monitor dialog when a super administrator clicks Add monitor', async () => {
    renderPage()

    await screen.findByText('No upstream monitors')
    fireEvent.click(screen.getByRole('button', { name: 'Add monitor' }))

    expect(
      await screen.findByRole('dialog', { name: 'Add upstream monitor' })
    ).toBeInTheDocument()
  })
})

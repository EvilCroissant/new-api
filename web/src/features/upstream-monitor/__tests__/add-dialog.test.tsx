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

import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { UpstreamMonitorAddDialog } =
  await import('../components/upstream-monitor-add-dialog')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

type ApiMethod = (url: string, data?: unknown) => Promise<{ data: unknown }>
type MockableApi = { post: ApiMethod }

const apiClient = api as unknown as MockableApi
const originalPost = apiClient.post
const queryClients: InstanceType<typeof QueryClient>[] = []

function renderDialog(): { onCreated: ReturnType<typeof vi.fn> } {
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  queryClients.push(queryClient)
  const onCreated = vi.fn(async () => undefined)

  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <UpstreamMonitorAddDialog
          open
          onOpenChange={() => undefined}
          onCreated={onCreated}
        />
      </I18nextProvider>
    </QueryClientProvider>
  )

  return { onCreated }
}

function setInput(label: string, value: string): void {
  fireEvent.input(screen.getByLabelText(label), { target: { value } })
}

afterEach(() => {
  apiClient.post = originalPost
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
})

describe('UpstreamMonitorAddDialog', () => {
  test('shows manual provider selection when detection cannot identify the upstream', async () => {
    apiClient.post = async (url) => {
      expect(url).toBe('/api/upstream-monitors/detect')
      return {
        data: {
          success: true,
          data: {
            base_url: 'https://monitor.example.com',
            detected: false,
          },
        },
      }
    }
    renderDialog()

    setInput('Upstream URL', 'https://monitor.example.com')
    fireEvent.click(screen.getByRole('button', { name: 'Detect' }))

    expect(
      await screen.findByText(
        'Detection was unavailable. Choose the upstream type manually.'
      )
    ).toBeInTheDocument()
  })

  test('uses the detected New API type and submits its user ID with the access token', async () => {
    const requests: Array<{ url: string; data: unknown }> = []
    apiClient.post = async (url, data) => {
      requests.push({ url, data })
      if (url === '/api/upstream-monitors/detect') {
        return {
          data: {
            success: true,
            data: {
              base_url: 'https://monitor.example.com',
              provider: 'newapi',
              detected: true,
            },
          },
        }
      }
      if (url === '/api/upstream-monitors/') {
        return {
          data: {
            success: true,
            data: {
              id: 1,
              name: 'monitor.example.com',
              base_url: 'https://monitor.example.com',
              provider: 'newapi',
            },
          },
        }
      }
      throw new Error(`Unexpected request: ${url}`)
    }
    const { onCreated } = renderDialog()

    setInput('Upstream URL', 'https://monitor.example.com')
    fireEvent.click(screen.getByRole('button', { name: 'Detect' }))

    expect(await screen.findByText('Detected: New API')).toBeInTheDocument()
    setInput('New API user ID', '42')
    setInput('Personal access token', 'pat-example')
    fireEvent.click(screen.getByRole('button', { name: 'Sync and add' }))

    await waitFor(() => expect(onCreated).toHaveBeenCalledOnce())
    expect(requests).toHaveLength(2)
    expect(requests[1]).toEqual({
      url: '/api/upstream-monitors/',
      data: {
        name: undefined,
        base_url: 'https://monitor.example.com',
        provider: 'newapi',
        new_api_user_id: 42,
        access_token: 'pat-example',
      },
    })
  })

  test('submits the access and refresh tokens for a detected Sub2API site', async () => {
    const requests: Array<{ url: string; data: unknown }> = []
    apiClient.post = async (url, data) => {
      requests.push({ url, data })
      if (url === '/api/upstream-monitors/detect') {
        return {
          data: {
            success: true,
            data: {
              base_url: 'https://monitor.example.com',
              provider: 'sub2api',
              detected: true,
            },
          },
        }
      }
      if (url === '/api/upstream-monitors/') {
        return {
          data: {
            success: true,
            data: {
              id: 1,
              name: 'monitor.example.com',
              base_url: 'https://monitor.example.com',
              provider: 'sub2api',
            },
          },
        }
      }
      throw new Error(`Unexpected request: ${url}`)
    }
    const { onCreated } = renderDialog()

    setInput('Upstream URL', 'https://monitor.example.com')
    fireEvent.click(screen.getByRole('button', { name: 'Detect' }))

    expect(await screen.findByText('Detected: Sub2API')).toBeInTheDocument()
    setInput('JWT', 'access-example')
    setInput('Refresh token', 'refresh-example')
    fireEvent.click(screen.getByRole('button', { name: 'Sync and add' }))

    await waitFor(() => expect(onCreated).toHaveBeenCalledOnce())
    expect(requests).toHaveLength(2)
    expect(requests[1]).toEqual({
      url: '/api/upstream-monitors/',
      data: {
        name: undefined,
        base_url: 'https://monitor.example.com',
        provider: 'sub2api',
        new_api_user_id: undefined,
        access_token: 'access-example',
        refresh_token: 'refresh-example',
      },
    })
  })
})

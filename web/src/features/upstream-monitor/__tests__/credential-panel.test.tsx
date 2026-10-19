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
import { describe, expect, test, vi } from 'vitest'

import type { UpstreamMonitor } from '../types'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { UpstreamMonitorCredentialPanel } =
  await import('../components/upstream-monitor-credential-panel')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

const monitor: UpstreamMonitor = {
  id: 1,
  name: 'Upstream',
  base_url: 'https://monitor.example.com',
  provider: 'sub2api',
  new_api_user_id: 0,
  api_key_configured: true,
  access_token_configured: true,
  refresh_token_configured: true,
  balance_usd: 20,
  balance_available: true,
  group_count: 0,
  pricing_count: 0,
  last_synced_at: 1,
  last_error: '',
  created_at: 1,
  updated_at: 1,
}

function renderPanel(isSaving = false) {
  const onSave = vi.fn(async () => undefined)
  render(
    <I18nextProvider i18n={i18n}>
      <UpstreamMonitorCredentialPanel
        monitor={monitor}
        id='credentials'
        isSaving={isSaving}
        onCancel={() => undefined}
        onSave={onSave}
      />
    </I18nextProvider>
  )
  return onSave
}

describe('UpstreamMonitorCredentialPanel', () => {
  test('submits auth_token through the existing access_token mapping and preserves refresh_token', async () => {
    const onSave = renderPanel()
    fireEvent.input(screen.getByLabelText('auth_token'), {
      target: { value: ' auth-new ' },
    })
    fireEvent.input(screen.getByLabelText('refresh_token'), {
      target: { value: ' refresh-new ' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save and sync' }))
    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith({
        access_token: 'auth-new',
        refresh_token: 'refresh-new',
      })
    )
  })
  test('replaces the configured key without submitting blank JWT or refresh credentials', async () => {
    const onSave = renderPanel()
    fireEvent.input(screen.getByLabelText('API key'), {
      target: { value: ' replacement-key ' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save and sync' }))
    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith({ api_key: 'replacement-key' })
    )
  })

  test('clears the configured key deliberately and keeps JWT fallback unchanged', async () => {
    const onSave = renderPanel()
    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Use an automatic channel key' })
    )
    expect(screen.getByLabelText('API key')).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Save and sync' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ api_key: '' }))
  })

  test('disables all credentials and automatic selection while saving', () => {
    renderPanel(true)
    for (const label of ['API key', 'auth_token', 'refresh_token']) {
      expect(screen.getByLabelText(label)).toBeDisabled()
    }
    expect(
      screen.getByRole('checkbox', { name: 'Use an automatic channel key' })
    ).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  })
})

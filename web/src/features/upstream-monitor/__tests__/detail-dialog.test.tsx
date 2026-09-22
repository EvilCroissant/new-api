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
import { describe, expect, test } from 'vitest'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { UpstreamMonitorGroupPanel } =
  await import('../components/upstream-monitor-group-panel')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

function renderGroupPanel(groups: unknown): void {
  render(
    <I18nextProvider i18n={i18n}>
      <UpstreamMonitorGroupPanel
        id='upstream-monitor-panel-1'
        monitor={{
          id: 1,
          name: 'monitor.example.com',
          base_url: 'https://monitor.example.com',
          provider: 'newapi',
          new_api_user_id: 1,
          access_token_configured: true,
          refresh_token_configured: false,
          balance_usd: 0,
          balance_available: false,
          group_count: 0,
          pricing_count: 0,
          groups,
          last_synced_at: 0,
          last_error: '',
          created_at: 0,
          updated_at: 0,
        }}
      />
    </I18nextProvider>
  )
}

describe('UpstreamMonitorGroupPanel', () => {
  test('shows every available group with the current user multiplier', async () => {
    renderGroupPanel({
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

    expect(await screen.findByText('Group')).toBeInTheDocument()
    expect(screen.getByText('Multiplier')).toBeInTheDocument()
    expect(screen.getByText('ChatGPT-Plus 【稳定通道】')).toBeInTheDocument()
    expect(screen.getByText('0.045x')).toBeInTheDocument()
    expect(screen.getByText('ChatGPT-Pro 【高并发通道】')).toBeInTheDocument()
    expect(screen.getByText('0.2x')).toBeInTheDocument()
    expect(screen.queryByText(/"groups"/)).not.toBeInTheDocument()
  })

  test('shows New API groups and multipliers', async () => {
    renderGroupPanel({
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
    expect(screen.getByText('0.2x')).toBeInTheDocument()
    expect(screen.getByText('auto')).toBeInTheDocument()
    expect(screen.getByText('自动')).toBeInTheDocument()
  })
})

import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, expect, test, vi } from 'vitest'

import type { UpstreamMonitor } from '@/features/upstream-monitor/types'
import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'

import type { ChannelProfitRow } from '../../types'
import { ProfitSettingsDialog } from '../profit-settings-dialog'
import { ProfitTable } from '../profit-table'

const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({ lng: 'en', resources: { en } })
})
function showDialog(monitor?: UpstreamMonitor) {
  const onSave = vi.fn().mockResolvedValue(undefined)
  const onClose = vi.fn()
  const row = {
    channel_id: 182,
    channel_name: 'Test upstream',
    base_url: 'https://example.com',
    cost_mode: 'ratio',
    cost_factor: 1,
    manual_ratio: null,
    request_cost_usd: null,
    sync_interval_minutes: 60,
  } as ChannelProfitRow
  render(
    <I18nextProvider i18n={i18n}>
      <ProfitSettingsDialog
        row={row}
        monitor={monitor}
        saving={false}
        onSave={onSave}
        onClose={onClose}
      />
    </I18nextProvider>
  )
  return { onSave, onClose }
}
test('blank upstream ratio saves automatic discovery', async () => {
  const { onSave, onClose } = showDialog()
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onClose).toHaveBeenCalled())
  expect(onSave).toHaveBeenCalledWith(
    182,
    expect.objectContaining({
      cost_mode: 'ratio',
      clear_manual_ratio: true,
      clear_request_cost: true,
    })
  )
})

test('existing New API account credentials share the profit settings and blank secrets are retained', async () => {
  const { onSave } = showDialog({
    id: 9,
    provider: 'newapi',
    new_api_user_id: 42,
    access_token_configured: true,
  } as UpstreamMonitor)
  expect(screen.getByLabelText('New API user ID')).toHaveValue(42)
  expect(screen.getByLabelText('Personal access token')).toHaveValue('')
  fireEvent.change(screen.getByLabelText('Personal access token'), {
    target: { value: 'updated-token' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith(182, expect.anything(), {
      provider: 'newapi',
      new_api_user_id: 42,
      access_token: 'updated-token',
    })
  )
})

test('Sub2API credentials and pricing can be saved together', async () => {
  const { onSave } = showDialog({
    id: 10,
    provider: 'sub2api',
    access_token_configured: true,
    refresh_token_configured: true,
  } as UpstreamMonitor)
  fireEvent.change(screen.getByLabelText('JWT'), {
    target: { value: 'new-jwt' },
  })
  fireEvent.change(screen.getByLabelText('Refresh token'), {
    target: { value: 'new-refresh' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith(182, expect.anything(), {
      provider: 'sub2api',
      access_token: 'new-jwt',
      refresh_token: 'new-refresh',
    })
  )
})
test('unchanged existing credentials are omitted when only pricing is saved', async () => {
  const { onSave } = showDialog({
    id: 9,
    provider: 'newapi',
    new_api_user_id: 42,
    access_token_configured: true,
  } as UpstreamMonitor)
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onSave).toHaveBeenCalled())
  expect(onSave.mock.calls[0]).toHaveLength(2)
})

test('detects the channel base URL and validates credentials before saving a new account', async () => {
  const detect = vi
    .spyOn(api, 'post')
    .mockResolvedValue({
      data: { success: true, data: { detected: true, provider: 'sub2api' } },
    })
  const { onSave } = showDialog()
  fireEvent.click(screen.getByRole('button', { name: 'Detect site' }))
  await screen.findByLabelText('JWT')
  expect(detect).toHaveBeenCalledWith(
    '/api/upstream-monitors/detect',
    { base_url: 'https://example.com' },
    expect.anything()
  )
  fireEvent.change(screen.getByLabelText('JWT'), {
    target: { value: 'new-jwt' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(onSave).not.toHaveBeenCalled()
  expect(screen.getByText('Refresh token is required')).toBeVisible()
  fireEvent.change(screen.getByLabelText('Refresh token'), {
    target: { value: 'new-refresh' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith(182, expect.anything(), {
      provider: 'sub2api',
      access_token: 'new-jwt',
      refresh_token: 'new-refresh',
    })
  )
})
test('explicit zero ratio is saved without becoming automatic', async () => {
  const { onSave } = showDialog()
  fireEvent.change(screen.getByLabelText('Upstream group ratio'), {
    target: { value: '0' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith(
      182,
      expect.objectContaining({ manual_ratio: 0 })
    )
  )
  expect(onSave.mock.calls[0][1]).not.toHaveProperty('clear_manual_ratio')
})
test('fixed mode requires a price and ignores an invalid hidden ratio', async () => {
  const { onSave } = showDialog()
  fireEvent.change(screen.getByLabelText('Upstream group ratio'), {
    target: { value: '-1' },
  })
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Cost calculation mode'), {
    target: { value: 'request' },
  })
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Fixed request cost (USD)'), {
    target: { value: '0' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith(
      182,
      expect.objectContaining({ cost_mode: 'request', request_cost_usd: 0 })
    )
  )
  expect(onSave.mock.calls[0][1]).not.toHaveProperty('manual_ratio')
})
test('failed save retains the form and entered values for retry', async () => {
  const { onSave, onClose } = showDialog()
  onSave.mockRejectedValueOnce(new Error('Network unavailable'))
  fireEvent.change(screen.getByLabelText('Upstream group ratio'), {
    target: { value: '0.005' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onSave).toHaveBeenCalled())
  expect(onClose).not.toHaveBeenCalled()
  expect(screen.getByRole('dialog')).toBeVisible()
  expect(screen.getByLabelText('Upstream group ratio')).toHaveValue(0.005)
})

function showTable(isRoot: boolean, monitor?: UpstreamMonitor) {
  const row: ChannelProfitRow = {
    group_id: 'upstream-182',
    channel_id: 182,
    channel_ids: [182],
    channel_names: ['Test upstream'],
    channel_name: 'Test upstream',
    base_url: 'https://example.com',
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
    profit_usd: 0,
    cost_available: false,
    profit_available: false,
    margin: 0,
    margin_available: false,
    partial: false,
    status: 'pending',
    downstream_rates: [],
    keys: [],
    request_coverage: {
      total: 0,
      matched: 0,
      estimated: 0,
      unknown: 0,
      revenue_usd: 0,
      cost_usd: 0,
    },
  }
  render(
    <I18nextProvider i18n={i18n}>
      <ProfitTable
        rows={[row]}
        monitors={monitor ? { 182: monitor } : undefined}
        isRoot={isRoot}
        onSync={vi.fn()}
        onSaveSettings={vi.fn().mockResolvedValue(undefined)}
      />
    </I18nextProvider>
  )
}

test.each(['click', 'keyboard'])(
  'root opens settings through the channel name using %s',
  async (interaction) => {
    showTable(true)
    const user = userEvent.setup()
    const trigger = screen.getByRole('button', { name: 'Settings' })
    expect(trigger).toHaveTextContent('Test upstream')
    expect(screen.queryByText('Settings')).not.toBeInTheDocument()
    if (interaction === 'keyboard') {
      trigger.focus()
      await user.keyboard('{Enter}')
    } else {
      await user.click(trigger)
    }
    expect(await screen.findByRole('dialog')).toBeVisible()
    expect(screen.getByLabelText('Display name')).toHaveValue('Test upstream')
  }
)

test('non-root sees the channel name without a settings trigger', () => {
  showTable(false)
  expect(screen.getByText('Test upstream')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Settings' })
  ).not.toBeInTheDocument()
})

test('balance is beside the channel name and opens the same settings with account fields', async () => {
  showTable(true, {
    id: 9,
    provider: 'newapi',
    new_api_user_id: 42,
    balance_available: true,
    balance_usd: 12.34,
  } as UpstreamMonitor)
  const balance = screen.getByLabelText('Available balance')
  expect(balance).toHaveTextContent('$12.34')
  expect(balance.parentElement).toContainElement(
    screen.getByRole('button', { name: 'Settings' })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
  expect(await screen.findByLabelText('New API user ID')).toHaveValue(42)
  expect(screen.getAllByRole('button', { name: 'Save' })).toHaveLength(1)
})

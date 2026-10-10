import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'

import type { ChannelProfitRow } from '../../types'
import { ProfitSettingsDialog } from '../profit-settings-dialog'

const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({ lng: 'en', resources: { en } })
})
function showDialog() {
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
  fireEvent.change(screen.getByRole('combobox'), {
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

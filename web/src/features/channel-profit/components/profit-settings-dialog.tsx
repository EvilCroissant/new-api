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

import { type FormEvent, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Spinner } from '@/components/ui/spinner'
import { detectUpstreamMonitor } from '@/features/upstream-monitor/api'
import { UpstreamAccountFields } from '@/features/upstream-monitor/components/upstream-account-fields'
import {
  upstreamMonitorFormSchema,
  upstreamMonitorCredentialFormSchema,
} from '@/features/upstream-monitor/lib/schema'
import type {
  UpstreamMonitor,
  UpstreamMonitorProvider,
  UpstreamMonitorUpdateInput,
} from '@/features/upstream-monitor/types'

import type { ChannelProfitConfigInput, ChannelProfitRow } from '../types'

type ProfitSettingsDialogProps = {
  row: ChannelProfitRow
  monitor?: UpstreamMonitor
  saving: boolean
  onClose: () => void
  onSave: (
    channelId: number,
    input: ChannelProfitConfigInput,
    account?: UpstreamMonitorUpdateInput & { provider: UpstreamMonitorProvider }
  ) => Promise<void>
}

export function ProfitSettingsDialog(props: ProfitSettingsDialogProps) {
  const { t } = useTranslation()
  const [displayName, setDisplayName] = useState(props.row.channel_name)
  const [syncInterval, setSyncInterval] = useState(
    String(props.row.sync_interval_minutes)
  )
  const [costMode, setCostMode] = useState<'ratio' | 'request'>(
    props.row.cost_mode ||
      (props.row.request_cost_usd == null ? 'ratio' : 'request')
  )
  const [manualRatio, setManualRatio] = useState(
    props.row.manual_ratio == null ? '' : String(props.row.manual_ratio)
  )
  const parsedManualRatio = Number(manualRatio)
  const isManualRatioValid =
    manualRatio.trim() === '' ||
    (Number.isFinite(parsedManualRatio) &&
      parsedManualRatio >= 0 &&
      parsedManualRatio <= 100)
  const [accessToken, setAccessToken] = useState('')
  const [refreshToken, setRefreshToken] = useState('')
  const [userID, setUserID] = useState(
    String(props.monitor?.new_api_user_id || '')
  )
  const [provider, setProvider] = useState<UpstreamMonitorProvider | ''>(
    props.monitor?.provider || ''
  )
  const [detecting, setDetecting] = useState(false)
  const [accountErrors, setAccountErrors] = useState<string[]>([])

  const handleDetect = async () => {
    setDetecting(true)
    try {
      const response = await detectUpstreamMonitor(props.row.base_url)
      if (!response.success) {
        throw new Error(response.message || t('Detection failed'))
      }
      if (response.data?.detected && response.data.provider) {
        setProvider(response.data.provider)
      } else {
        toast.info(t('Could not identify this site. Select its type manually.'))
      }
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Detection failed')
      )
    } finally {
      setDetecting(false)
    }
  }
  const [costFactor, setCostFactor] = useState(
    String(props.row.cost_factor || 1)
  )
  const [requestCost, setRequestCost] = useState(
    props.row.request_cost_usd == null ? '' : String(props.row.request_cost_usd)
  )

  const parsedInterval = Number(syncInterval)
  const isIntervalValid =
    Number.isInteger(parsedInterval) &&
    parsedInterval >= 1 &&
    parsedInterval <= 10080

  const isDisplayNameValid = displayName.trim().length > 0
  const parsedFactor = Number(costFactor)
  const parsedRequestCost = Number(requestCost)
  const isFactorValid =
    Number.isFinite(parsedFactor) && parsedFactor > 0 && parsedFactor <= 100
  const isRequestCostValid =
    costMode !== 'request' ||
    (requestCost.trim() !== '' &&
      Number.isFinite(parsedRequestCost) &&
      parsedRequestCost >= 0 &&
      parsedRequestCost <= 1000000)
  const isFormValid =
    isIntervalValid &&
    isDisplayNameValid &&
    isFactorValid &&
    isRequestCostValid &&
    (costMode !== 'ratio' || isManualRatioValid)

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault()
    if (!isFormValid || props.saving || detecting) return

    const input: ChannelProfitConfigInput = {
      display_name: displayName.trim(),
      sync_interval_minutes: parsedInterval,
      cost_factor: parsedFactor,
      cost_mode: costMode,
      ...(costMode !== 'request'
        ? { clear_request_cost: true }
        : { request_cost_usd: parsedRequestCost }),
    }

    if (costMode === 'ratio') {
      if (manualRatio.trim() === '') input.clear_manual_ratio = true
      else input.manual_ratio = parsedManualRatio
    }

    let account:
      | (UpstreamMonitorUpdateInput & { provider: UpstreamMonitorProvider })
      | undefined
    const credentialsChanged =
      accessToken.trim() !== '' ||
      refreshToken.trim() !== '' ||
      (props.monitor?.provider === 'newapi' &&
        Number(userID) !== props.monitor.new_api_user_id)
    if (provider && (!props.monitor || credentialsChanged)) {
      const values = {
        provider,
        new_api_user_id: provider === 'newapi' ? Number(userID) : undefined,
        access_token: accessToken.trim(),
        refresh_token: refreshToken.trim(),
      }
      const result = props.monitor
        ? upstreamMonitorCredentialFormSchema.safeParse(values)
        : upstreamMonitorFormSchema.safeParse({
            ...values,
            base_url: props.row.base_url,
          })
      if (!result.success) {
        setAccountErrors(result.error.issues.map((issue) => issue.message))
        return
      }
      account = {
        provider,
        ...(provider === 'newapi' ? { new_api_user_id: Number(userID) } : {}),
        ...(accessToken.trim() ? { access_token: accessToken.trim() } : {}),
        ...(provider === 'sub2api' && refreshToken.trim()
          ? { refresh_token: refreshToken.trim() }
          : {}),
      }
    }
    setAccountErrors([])

    try {
      if (account) await props.onSave(props.row.channel_id, input, account)
      else await props.onSave(props.row.channel_id, input)
      props.onClose()
    } catch {
      // The mutation displays the error; keep the form open for correction.
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) =>
        !open && !props.saving && !detecting && props.onClose()
      }
    >
      <DialogContent className='max-h-[85dvh] overflow-y-auto rounded-xl sm:max-w-md'>
        <form onSubmit={(e) => void handleSubmit(e)}>
          <DialogHeader>
            <DialogTitle className='text-base font-semibold'>
              {t('Profit monitoring settings')}
            </DialogTitle>
            <DialogDescription className='text-muted-foreground/80 mt-1 truncate font-mono text-xs'>
              {props.row.base_url}
            </DialogDescription>
          </DialogHeader>

          <FieldGroup className='my-4 space-y-3.5'>
            <Field>
              <FieldLabel htmlFor='profit-account-provider'>
                {t('Site type')}
              </FieldLabel>
              <div className='flex flex-wrap gap-2'>
                <NativeSelect
                  id='profit-account-provider'
                  value={provider}
                  disabled={props.saving || detecting || !!props.monitor}
                  onChange={(event) => {
                    setProvider(event.target.value as UpstreamMonitorProvider)
                    setAccessToken('')
                    setRefreshToken('')
                  }}
                >
                  <NativeSelectOption value=''>
                    {t('Not configured')}
                  </NativeSelectOption>
                  <NativeSelectOption value='newapi'>
                    New API
                  </NativeSelectOption>
                  <NativeSelectOption value='sub2api'>
                    Sub2API
                  </NativeSelectOption>
                </NativeSelect>
                {!props.monitor && (
                  <Button
                    type='button'
                    variant='outline'
                    disabled={props.saving || detecting}
                    onClick={() => void handleDetect()}
                  >
                    {detecting && <Spinner aria-hidden='true' />}
                    {t('Detect site')}
                  </Button>
                )}
              </div>
            </Field>
            {provider && (
              <UpstreamAccountFields
                provider={provider}
                id='profit-account'
                disabled={props.saving || detecting}
                existing={!!props.monitor}
                accessTokenConfigured={props.monitor?.access_token_configured}
                refreshTokenConfigured={props.monitor?.refresh_token_configured}
                userID={{
                  value: userID,
                  onChange: (event) => setUserID(event.target.value),
                }}
                accessToken={{
                  value: accessToken,
                  onChange: (event) => setAccessToken(event.target.value),
                }}
                refreshToken={{
                  value: refreshToken,
                  onChange: (event) => setRefreshToken(event.target.value),
                }}
              />
            )}
            {accountErrors.map((message) => (
              <FieldError key={message}>{t(message)}</FieldError>
            ))}
            <Field>
              <FieldLabel htmlFor='profit-cost-mode'>
                {t('Cost calculation mode')}
              </FieldLabel>
              <NativeSelect
                id='profit-cost-mode'
                value={costMode}
                disabled={props.saving}
                onChange={(event) =>
                  setCostMode(event.target.value as 'ratio' | 'request')
                }
              >
                <NativeSelectOption value='ratio'>
                  {t('Usage and upstream ratio')}
                </NativeSelectOption>
                <NativeSelectOption value='request'>
                  {t('Fixed request cost (USD)')}
                </NativeSelectOption>
              </NativeSelect>
              <FieldDescription>
                {t('Cost settings apply to new requests.')}
              </FieldDescription>
            </Field>
            {costMode === 'ratio' && (
              <Field data-invalid={!isManualRatioValid}>
                <FieldLabel htmlFor='profit-manual-ratio'>
                  {t('Upstream group ratio')}
                </FieldLabel>
                <Input
                  id='profit-manual-ratio'
                  type='number'
                  min={0}
                  max={100}
                  step='any'
                  value={manualRatio}
                  onChange={(event) => setManualRatio(event.target.value)}
                  placeholder={t('Automatic')}
                  disabled={props.saving}
                  aria-invalid={!isManualRatioValid}
                />
                <FieldDescription>
                  {t(
                    'Leave blank to use the ratio from upstream logs. Zero means free upstream usage.'
                  )}
                </FieldDescription>
              </Field>
            )}

            <Field data-invalid={!isFactorValid}>
              <FieldLabel htmlFor='profit-cost-factor'>
                {t('Purchase cost factor')}
              </FieldLabel>
              <Input
                id='profit-cost-factor'
                type='number'
                min={0.000001}
                max={100}
                step='any'
                value={costFactor}
                onChange={(event) => setCostFactor(event.target.value)}
                aria-invalid={!isFactorValid}
                disabled={props.saving}
                required
              />
            </Field>
            {costMode === 'request' && (
              <Field data-invalid={!isRequestCostValid}>
                <FieldLabel htmlFor='profit-request-cost'>
                  {t('Fixed request cost (USD)')}
                </FieldLabel>
                <Input
                  id='profit-request-cost'
                  type='number'
                  min={0}
                  max={1000000}
                  step='any'
                  value={requestCost}
                  onChange={(event) => setRequestCost(event.target.value)}
                  aria-invalid={!isRequestCostValid}
                  disabled={props.saving}
                />
              </Field>
            )}
            <Field data-invalid={!isDisplayNameValid}>
              <FieldLabel
                htmlFor='profit-display-name'
                className='text-xs font-medium'
              >
                {t('Display name')}
              </FieldLabel>
              <Input
                id='profit-display-name'
                value={displayName}
                maxLength={100}
                onChange={(event) => setDisplayName(event.target.value)}
                disabled={props.saving}
                aria-invalid={!isDisplayNameValid}
                required
              />
            </Field>

            <Field data-invalid={!isIntervalValid}>
              <FieldLabel
                htmlFor='profit-sync-interval'
                className='text-xs font-medium'
              >
                {t('Automatic sync interval')}
              </FieldLabel>
              <div className='flex items-center gap-2'>
                <Input
                  id='profit-sync-interval'
                  type='number'
                  min={1}
                  max={10080}
                  step={1}
                  value={syncInterval}
                  onChange={(event) => setSyncInterval(event.target.value)}
                  aria-invalid={!isIntervalValid}
                  disabled={props.saving}
                  required
                />
                <span className='text-muted-foreground shrink-0 text-xs'>
                  {t('minutes')}
                </span>
              </div>
            </Field>
          </FieldGroup>

          <DialogFooter className='pt-2'>
            <Button
              type='button'
              variant='outline'
              onClick={props.onClose}
              disabled={props.saving || detecting}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='submit'
              disabled={props.saving || detecting || !isFormValid}
            >
              {props.saving && (
                <Spinner data-icon='inline-start' aria-label={t('Loading')} />
              )}
              {t('Save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

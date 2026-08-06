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

import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'

import type { ChannelProfitConfigInput, ChannelProfitRow } from '../types'

type ProfitSettingsDialogProps = {
  row: ChannelProfitRow
  saving: boolean
  onClose: () => void
  onSave: (channelId: number, input: ChannelProfitConfigInput) => Promise<void>
}

export function ProfitSettingsDialog(props: ProfitSettingsDialogProps) {
  const { t } = useTranslation()
  const [displayName, setDisplayName] = useState(props.row.channel_name)
  const [syncInterval, setSyncInterval] = useState(
    String(props.row.sync_interval_minutes)
  )
  const [accessToken, setAccessToken] = useState('')
  const [accessTokenChanged, setAccessTokenChanged] = useState(false)

  const parsedInterval = Number(syncInterval)
  const intervalValid =
    Number.isInteger(parsedInterval) &&
    parsedInterval >= 1 &&
    parsedInterval <= 10080

  const handleSave = async () => {
    if (!intervalValid) return
    const input: ChannelProfitConfigInput = {
      display_name: displayName.trim(),
      sync_interval_minutes: parsedInterval,
    }
    if (accessTokenChanged) {
      input.access_token = accessToken.trim()
    }
    await props.onSave(props.row.channel_id, input)
    props.onClose()
  }

  return (
    <Dialog open onOpenChange={(open) => !open && props.onClose()}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('Profit monitoring settings')}</DialogTitle>
          <DialogDescription className='truncate font-mono text-xs'>
            {props.row.base_url}
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor='profit-display-name'>
              {t('Display name')}
            </FieldLabel>
            <Input
              id='profit-display-name'
              value={displayName}
              maxLength={100}
              onChange={(event) => setDisplayName(event.target.value)}
              disabled={props.saving}
            />
          </Field>
          <Field data-invalid={!intervalValid}>
            <FieldLabel htmlFor='profit-sync-interval'>
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
                aria-invalid={!intervalValid}
                disabled={props.saving}
              />
              <span className='text-muted-foreground shrink-0 text-sm'>
                {t('minutes')}
              </span>
            </div>
          </Field>
          {props.row.provider === 'new_api' && (
            <Field>
              <FieldLabel htmlFor='profit-access-token'>
                {t('Access token')}
              </FieldLabel>
              <Input
                id='profit-access-token'
                type='password'
                value={accessToken}
                placeholder={
                  props.row.access_token_configured && !accessTokenChanged
                    ? t('Access token configured')
                    : t('Enter access token')
                }
                onChange={(event) => {
                  setAccessToken(event.target.value)
                  setAccessTokenChanged(true)
                }}
                disabled={props.saving}
              />
              {props.row.access_token_configured && !accessTokenChanged && (
                <Button
                  type='button'
                  variant='link'
                  size='xs'
                  className='h-auto px-0'
                  onClick={() => {
                    setAccessToken('')
                    setAccessTokenChanged(true)
                  }}
                  disabled={props.saving}
                >
                  {t('Clear access token')}
                </Button>
              )}
            </Field>
          )}
        </FieldGroup>
        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={props.onClose}
            disabled={props.saving}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            onClick={() => void handleSave()}
            disabled={props.saving || !intervalValid}
          >
            {props.saving && (
              <Spinner data-icon='inline-start' aria-label={t('Loading')} />
            )}
            {t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

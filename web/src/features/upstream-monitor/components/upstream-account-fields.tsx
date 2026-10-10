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

import type { ComponentPropsWithRef } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import type { UpstreamMonitorProvider } from '../types'

type UpstreamAccountFieldsProps = {
  provider: UpstreamMonitorProvider
  id: string
  disabled: boolean
  existing: boolean
  accessTokenConfigured?: boolean
  refreshTokenConfigured?: boolean
  userID: ComponentPropsWithRef<'input'>
  accessToken: ComponentPropsWithRef<'input'>
  refreshToken: ComponentPropsWithRef<'input'>
}

export function UpstreamAccountFields(props: UpstreamAccountFieldsProps) {
  const { t } = useTranslation()
  return (
    <>
      {props.provider === 'newapi' && (
        <Field>
          <FieldLabel htmlFor={`${props.id}-user-id`}>
            {t('New API user ID')}
          </FieldLabel>
          <Input
            id={`${props.id}-user-id`}
            type='number'
            min={1}
            step={1}
            disabled={props.disabled}
            {...props.userID}
          />
        </Field>
      )}
      <Field>
        <div className='flex items-center justify-between gap-2'>
          <FieldLabel htmlFor={`${props.id}-access-token`}>
            {props.provider === 'newapi'
              ? t('Personal access token')
              : t('JWT')}
          </FieldLabel>
          <Badge variant='outline'>
            {props.accessTokenConfigured
              ? t('Configured')
              : t('Not configured')}
          </Badge>
        </div>
        <Input
          id={`${props.id}-access-token`}
          type='password'
          autoComplete='off'
          disabled={props.disabled}
          {...props.accessToken}
        />
        {props.existing && (
          <FieldDescription>
            {t('Leave blank to keep the existing credential')}
          </FieldDescription>
        )}
      </Field>
      {props.provider === 'sub2api' && (
        <Field>
          <div className='flex items-center justify-between gap-2'>
            <FieldLabel htmlFor={`${props.id}-refresh-token`}>
              {t('Refresh token')}
            </FieldLabel>
            <Badge variant='outline'>
              {props.refreshTokenConfigured
                ? t('Configured')
                : t('Not configured')}
            </Badge>
          </div>
          <Input
            id={`${props.id}-refresh-token`}
            type='password'
            autoComplete='off'
            disabled={props.disabled}
            {...props.refreshToken}
          />
          {props.existing && (
            <FieldDescription>
              {t('Leave blank to keep the existing credential')}
            </FieldDescription>
          )}
        </Field>
      )}
    </>
  )
}

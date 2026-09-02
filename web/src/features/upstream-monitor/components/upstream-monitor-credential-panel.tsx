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

import { zodResolver } from '@hookform/resolvers/zod'
import { Key01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'

import {
  upstreamMonitorCredentialFormSchema,
  type UpstreamMonitorCredentialFormInput,
  type UpstreamMonitorCredentialFormValues,
} from '../lib/schema'
import type { UpstreamMonitor, UpstreamMonitorUpdateInput } from '../types'

type UpstreamMonitorCredentialPanelProps = {
  monitor: UpstreamMonitor
  id: string
  isSaving: boolean
  onCancel: () => void
  onSave: (input: UpstreamMonitorUpdateInput) => Promise<void>
}

export function UpstreamMonitorCredentialPanel(
  props: UpstreamMonitorCredentialPanelProps
) {
  const { t } = useTranslation()
  const form = useForm<
    UpstreamMonitorCredentialFormInput,
    unknown,
    UpstreamMonitorCredentialFormValues
  >({
    resolver: zodResolver(upstreamMonitorCredentialFormSchema),
    defaultValues: {
      provider: props.monitor.provider,
      new_api_user_id:
        props.monitor.provider === 'newapi'
          ? props.monitor.new_api_user_id
          : undefined,
      access_token: '',
      refresh_token: '',
    },
  })

  const handleSubmit = async (values: UpstreamMonitorCredentialFormValues) => {
    const input: UpstreamMonitorUpdateInput = {}
    if (values.provider === 'newapi') {
      input.new_api_user_id = values.new_api_user_id
    }
    if (values.access_token) {
      input.access_token = values.access_token
    }
    if (values.provider === 'sub2api' && values.refresh_token) {
      input.refresh_token = values.refresh_token
    }

    try {
      await props.onSave(input)
      props.onCancel()
    } catch {
      return
    }
  }

  const providerLabel =
    props.monitor.provider === 'newapi' ? 'New API' : 'Sub2API'

  return (
    <section
      id={props.id}
      role='region'
      aria-label={t('Edit credentials')}
      className='bg-muted/25 animate-in fade-in slide-in-from-top-1 border-border/60 border-t px-4 py-4 duration-150'
    >
      <form
        onSubmit={(event) => void form.handleSubmit(handleSubmit)(event)}
        className='mx-auto max-w-3xl'
      >
        <input type='hidden' {...form.register('provider')} />
        <div className='mb-4 flex flex-wrap items-center gap-2'>
          <HugeiconsIcon
            icon={Key01Icon}
            strokeWidth={2}
            className='text-muted-foreground size-4'
            aria-hidden='true'
          />
          <h3 className='text-sm font-medium'>{t('Edit credentials')}</h3>
          <Badge variant='secondary' className='font-normal'>
            {providerLabel}
          </Badge>
          <span className='text-muted-foreground min-w-0 truncate font-mono text-xs'>
            {props.monitor.base_url}
          </span>
        </div>

        <FieldGroup className='gap-4 md:grid md:grid-cols-2'>
          {props.monitor.provider === 'newapi' && (
            <Field
              data-invalid={Boolean(form.formState.errors.new_api_user_id)}
            >
              <FieldLabel
                htmlFor={`upstream-monitor-user-id-${props.monitor.id}`}
              >
                {t('New API user ID')}
              </FieldLabel>
              <Input
                id={`upstream-monitor-user-id-${props.monitor.id}`}
                type='number'
                min={1}
                step={1}
                inputMode='numeric'
                disabled={props.isSaving}
                {...form.register('new_api_user_id')}
              />
              <FieldError errors={[form.formState.errors.new_api_user_id]}>
                {form.formState.errors.new_api_user_id?.message &&
                  t(form.formState.errors.new_api_user_id.message)}
              </FieldError>
            </Field>
          )}

          <Field data-invalid={Boolean(form.formState.errors.access_token)}>
            <div className='flex items-center justify-between gap-2'>
              <FieldLabel
                htmlFor={`upstream-monitor-access-token-${props.monitor.id}`}
              >
                {props.monitor.provider === 'newapi'
                  ? t('Personal access token')
                  : t('JWT')}
              </FieldLabel>
              <Badge
                variant={
                  props.monitor.access_token_configured ? 'outline' : 'warning'
                }
                className='h-5 font-normal'
              >
                {props.monitor.access_token_configured
                  ? t('Configured')
                  : t('Not configured')}
              </Badge>
            </div>
            <Input
              id={`upstream-monitor-access-token-${props.monitor.id}`}
              type='password'
              autoComplete='off'
              disabled={props.isSaving}
              {...form.register('access_token')}
            />
            <FieldDescription>
              {t('Leave blank to keep the existing credential')}
            </FieldDescription>
            <FieldError errors={[form.formState.errors.access_token]}>
              {form.formState.errors.access_token?.message &&
                t(form.formState.errors.access_token.message)}
            </FieldError>
          </Field>

          {props.monitor.provider === 'sub2api' && (
            <Field data-invalid={Boolean(form.formState.errors.refresh_token)}>
              <div className='flex items-center justify-between gap-2'>
                <FieldLabel
                  htmlFor={`upstream-monitor-refresh-token-${props.monitor.id}`}
                >
                  {t('Refresh token')}
                </FieldLabel>
                <Badge
                  variant={
                    props.monitor.refresh_token_configured
                      ? 'outline'
                      : 'warning'
                  }
                  className='h-5 font-normal'
                >
                  {props.monitor.refresh_token_configured
                    ? t('Configured')
                    : t('Not configured')}
                </Badge>
              </div>
              <Input
                id={`upstream-monitor-refresh-token-${props.monitor.id}`}
                type='password'
                autoComplete='off'
                disabled={props.isSaving}
                {...form.register('refresh_token')}
              />
              <FieldDescription>
                {t('Leave blank to keep the existing credential')}
              </FieldDescription>
              <FieldError errors={[form.formState.errors.refresh_token]}>
                {form.formState.errors.refresh_token?.message &&
                  t(form.formState.errors.refresh_token.message)}
              </FieldError>
            </Field>
          )}
        </FieldGroup>

        <div className='mt-4 flex justify-end gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={props.isSaving}
            onClick={props.onCancel}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' disabled={props.isSaving}>
            {props.isSaving && (
              <Spinner data-icon='inline-start' aria-hidden='true' />
            )}
            {props.isSaving ? t('Saving...') : t('Save and sync')}
          </Button>
        </div>
      </form>
    </section>
  )
}

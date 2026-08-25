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
import { useMutation } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'

import { createUpstreamMonitor, detectUpstreamMonitor } from '../api'
import {
  upstreamMonitorFormSchema,
  type UpstreamMonitorFormInput,
  type UpstreamMonitorFormValues,
} from '../lib/schema'
import type { UpstreamMonitor, UpstreamMonitorProvider } from '../types'

type DetectionState = 'idle' | 'detected' | 'manual'

type UpstreamMonitorAddDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (monitor: UpstreamMonitor) => Promise<void>
}

const providerItems = [
  { value: 'newapi', label: 'New API' },
  { value: 'sub2api', label: 'Sub2API' },
]

function getErrorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback
}

export function UpstreamMonitorAddDialog(props: UpstreamMonitorAddDialogProps) {
  const { t } = useTranslation()
  const [detectionState, setDetectionState] = useState<DetectionState>('idle')
  const form = useForm<
    UpstreamMonitorFormInput,
    unknown,
    UpstreamMonitorFormValues
  >({
    resolver: zodResolver(upstreamMonitorFormSchema),
    defaultValues: {
      name: '',
      base_url: '',
      provider: undefined,
      new_api_user_id: undefined,
      access_token: '',
      refresh_token: '',
    },
  })
  const provider = form.watch('provider')

  useEffect(() => {
    if (!props.open) return
    form.reset({
      name: '',
      base_url: '',
      provider: undefined,
      new_api_user_id: undefined,
      access_token: '',
      refresh_token: '',
    })
    setDetectionState('idle')
  }, [form, props.open])

  const detectMutation = useMutation({
    mutationFn: detectUpstreamMonitor,
    onSuccess: (response) => {
      if (!response.success) {
        setDetectionState('manual')
        toast.error(response.message || t('Detection failed'))
        return
      }
      if (response.data?.detected && response.data.provider) {
        form.setValue('base_url', response.data.base_url, {
          shouldValidate: true,
        })
        form.setValue('provider', response.data.provider, {
          shouldValidate: true,
        })
        setDetectionState('detected')
        return
      }
      form.setValue('provider', undefined)
      setDetectionState('manual')
      toast.info(t('Could not identify this site. Select its type manually.'))
    },
    onError: (error) => {
      form.setValue('provider', undefined)
      setDetectionState('manual')
      toast.error(getErrorMessage(error, t('Detection failed')))
    },
  })

  const createMutation = useMutation({
    mutationFn: createUpstreamMonitor,
    onSuccess: async (response) => {
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to add monitor'))
      }
      await props.onCreated(response.data)
      toast.success(t('Monitor added'))
      props.onOpenChange(false)
    },
    onError: (error) => {
      toast.error(getErrorMessage(error, t('Failed to add monitor')))
    },
  })

  const handleDetect = async () => {
    const isURLValid = await form.trigger('base_url')
    if (!isURLValid) return
    detectMutation.mutate(form.getValues('base_url'))
  }

  const handleSubmit = (values: UpstreamMonitorFormValues) => {
    if (!values.provider) return
    const input = {
      name: values.name?.trim() || undefined,
      base_url: values.base_url.trim(),
      provider: values.provider,
      new_api_user_id:
        values.provider === 'newapi' ? values.new_api_user_id : undefined,
      access_token: values.access_token.trim(),
    }
    if (values.provider === 'sub2api') {
      createMutation.mutate({
        ...input,
        refresh_token: values.refresh_token?.trim(),
      })
      return
    }
    createMutation.mutate(input)
  }

  const resetDetection = () => {
    form.setValue('provider', undefined)
    setDetectionState('idle')
  }

  const isSaving = createMutation.isPending
  const isBusy = isSaving || detectMutation.isPending
  const providerLabel = provider === 'newapi' ? 'New API' : 'Sub2API'
  const canSelectProvider = detectionState === 'manual'

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => !isBusy && props.onOpenChange(open)}
    >
      <DialogContent className='sm:max-w-lg'>
        <form onSubmit={(event) => void form.handleSubmit(handleSubmit)(event)}>
          <DialogHeader>
            <DialogTitle>{t('Add upstream monitor')}</DialogTitle>
            <DialogDescription>
              {t(
                'Monitor an independent upstream account without linking local channels or API keys.'
              )}
            </DialogDescription>
          </DialogHeader>

          <FieldGroup className='my-5 gap-4'>
            <Field>
              <FieldLabel htmlFor='upstream-monitor-name'>
                {t('Monitor name')}
              </FieldLabel>
              <Input
                id='upstream-monitor-name'
                maxLength={100}
                disabled={isBusy}
                {...form.register('name')}
              />
              <FieldDescription>
                {t('Optional; defaults to the site hostname.')}
              </FieldDescription>
              <FieldError errors={[form.formState.errors.name]}>
                {form.formState.errors.name?.message &&
                  t(form.formState.errors.name.message)}
              </FieldError>
            </Field>

            <Field>
              <FieldLabel htmlFor='upstream-monitor-url'>
                {t('Upstream URL')}
              </FieldLabel>
              <div className='flex gap-2'>
                <Input
                  id='upstream-monitor-url'
                  type='url'
                  autoComplete='url'
                  placeholder='https://example.com'
                  disabled={isBusy}
                  {...form.register('base_url', {
                    onChange: resetDetection,
                  })}
                />
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => void handleDetect()}
                  disabled={isBusy}
                >
                  {detectMutation.isPending && (
                    <Spinner
                      data-icon='inline-start'
                      aria-label={t('Loading')}
                    />
                  )}
                  {detectMutation.isPending ? t('Detecting...') : t('Detect')}
                </Button>
              </div>
              <FieldError errors={[form.formState.errors.base_url]}>
                {form.formState.errors.base_url?.message &&
                  t(form.formState.errors.base_url.message)}
              </FieldError>
            </Field>

            {detectionState === 'detected' && provider && (
              <Field>
                <FieldLabel>{t('Upstream type')}</FieldLabel>
                <div className='bg-muted rounded-lg border px-3 py-2 text-sm font-medium'>
                  {t('Detected: {{provider}}', { provider: providerLabel })}
                </div>
              </Field>
            )}

            {canSelectProvider && (
              <Field data-invalid={Boolean(form.formState.errors.provider)}>
                <FieldLabel htmlFor='upstream-monitor-provider'>
                  {t('Upstream type')}
                </FieldLabel>
                <Select<UpstreamMonitorProvider>
                  items={providerItems}
                  value={provider ?? null}
                  onValueChange={(value) => {
                    form.setValue('provider', value ?? undefined, {
                      shouldValidate: true,
                    })
                  }}
                  disabled={isBusy}
                >
                  <SelectTrigger
                    id='upstream-monitor-provider'
                    className='w-full'
                  >
                    <SelectValue placeholder={t('Select upstream type')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectItem value='newapi'>New API</SelectItem>
                      <SelectItem value='sub2api'>Sub2API</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FieldDescription>
                  {t(
                    'Detection was unavailable. Choose the upstream type manually.'
                  )}
                </FieldDescription>
                <FieldError errors={[form.formState.errors.provider]}>
                  {form.formState.errors.provider?.message &&
                    t(form.formState.errors.provider.message)}
                </FieldError>
              </Field>
            )}

            {provider && (
              <>
                {provider === 'newapi' && (
                  <Field
                    data-invalid={Boolean(
                      form.formState.errors.new_api_user_id
                    )}
                  >
                    <FieldLabel htmlFor='upstream-monitor-user-id'>
                      {t('New API user ID')}
                    </FieldLabel>
                    <Input
                      id='upstream-monitor-user-id'
                      type='number'
                      min={1}
                      step={1}
                      inputMode='numeric'
                      disabled={isBusy}
                      {...form.register('new_api_user_id')}
                    />
                    <FieldError
                      errors={[form.formState.errors.new_api_user_id]}
                    >
                      {form.formState.errors.new_api_user_id?.message &&
                        t(form.formState.errors.new_api_user_id.message)}
                    </FieldError>
                  </Field>
                )}

                <Field
                  data-invalid={Boolean(form.formState.errors.access_token)}
                >
                  <FieldLabel htmlFor='upstream-monitor-credential'>
                    {provider === 'newapi'
                      ? t('Personal access token')
                      : t('JWT')}
                  </FieldLabel>
                  <Input
                    id='upstream-monitor-credential'
                    type='password'
                    autoComplete='off'
                    placeholder={
                      provider === 'newapi'
                        ? t('Enter access token')
                        : t('Enter JWT')
                    }
                    disabled={isBusy}
                    {...form.register('access_token')}
                  />
                  <FieldDescription>
                    {t(
                      'The credential is stored server-side and is never returned to the browser.'
                    )}
                  </FieldDescription>
                  <FieldError errors={[form.formState.errors.access_token]}>
                    {form.formState.errors.access_token?.message &&
                      t(form.formState.errors.access_token.message)}
                  </FieldError>
                </Field>

                {provider === 'sub2api' && (
                  <Field
                    data-invalid={Boolean(form.formState.errors.refresh_token)}
                  >
                    <FieldLabel htmlFor='upstream-monitor-refresh-token'>
                      {t('Refresh token')}
                    </FieldLabel>
                    <Input
                      id='upstream-monitor-refresh-token'
                      type='password'
                      autoComplete='off'
                      disabled={isBusy}
                      {...form.register('refresh_token')}
                    />
                    <FieldDescription>
                      {t(
                        'The refresh token renews the Sub2API login session automatically.'
                      )}
                    </FieldDescription>
                    <FieldError errors={[form.formState.errors.refresh_token]}>
                      {form.formState.errors.refresh_token?.message &&
                        t(form.formState.errors.refresh_token.message)}
                    </FieldError>
                  </Field>
                )}
              </>
            )}
          </FieldGroup>

          <DialogFooter>
            <Button
              type='button'
              variant='outline'
              disabled={isBusy}
              onClick={() => props.onOpenChange(false)}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={isBusy || !provider}>
              {isSaving && (
                <Spinner data-icon='inline-start' aria-label={t('Loading')} />
              )}
              {isSaving ? t('Syncing...') : t('Sync and add')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

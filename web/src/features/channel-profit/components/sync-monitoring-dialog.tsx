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

import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Switch } from '@/components/ui/switch'

import type { ChannelProfitRow } from '../types'

type SyncMonitoringDialogProps = {
  rows: ChannelProfitRow[]
  togglingChannelId?: number
  onToggle: (channelId: number, enabled: boolean) => void
  onClose: () => void
}

export function SyncMonitoringDialog(props: SyncMonitoringDialogProps) {
  const { t } = useTranslation()

  return (
    <Dialog open onOpenChange={(open) => !open && props.onClose()}>
      <DialogContent className='rounded-xl sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle className='text-base font-semibold'>
            {t('Sync settings')}
          </DialogTitle>
          <DialogDescription className='text-muted-foreground text-xs'>
            {t(
              'Enable or disable profit monitoring and automatic sync for channels'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='my-3 max-h-[380px] space-y-2 overflow-y-auto pr-1'>
          {props.rows.map((row) => (
            <div
              key={row.group_id}
              className='bg-muted/20 hover:bg-muted/30 border-border/50 flex items-center justify-between rounded-lg border p-3 transition-colors'
            >
              <div className='min-w-0 pr-3'>
                <div className='flex items-center gap-2'>
                  <span className='text-foreground truncate text-sm font-semibold'>
                    {row.channel_name}
                  </span>
                  {row.provider && (
                    <Badge
                      variant='outline'
                      className='h-4 px-1.5 py-0 text-[10px] font-normal'
                    >
                      {row.provider}
                    </Badge>
                  )}
                </div>
                <p className='text-muted-foreground mt-0.5 truncate font-mono text-xs'>
                  {row.base_url}
                </p>
              </div>

              <Switch
                size='sm'
                checked={row.enabled}
                disabled={props.togglingChannelId === row.channel_id}
                onCheckedChange={(enabled) =>
                  props.onToggle(row.channel_id, enabled)
                }
                aria-label={
                  row.enabled ? t('Disable monitoring') : t('Enable monitoring')
                }
              />
            </div>
          ))}
        </div>

        <DialogFooter>
          <Button type='button' variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

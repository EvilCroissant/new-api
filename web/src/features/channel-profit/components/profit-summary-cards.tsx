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

import {
  BadgeDollarSign,
  CirclePercent,
  ReceiptText,
  TrendingUp,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Card, CardContent } from '@/components/ui/card'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { cn } from '@/lib/utils'

import type { ChannelProfitSummary } from '../types'

type Metric = {
  label: string
  value: string
  icon: LucideIcon
  tone?: string
}

type ProfitSummaryCardsProps = {
  summary: ChannelProfitSummary
}

export function ProfitSummaryCards(props: ProfitSummaryCardsProps) {
  const { t } = useTranslation()
  const profitTone =
    props.summary.profit_available && props.summary.profit_usd < 0
      ? 'text-destructive'
      : 'text-emerald-600 dark:text-emerald-400'
  const metrics: Metric[] = [
    {
      label: t('Downstream revenue'),
      value: formatBillingCurrencyFromUSD(props.summary.revenue_usd),
      icon: TrendingUp,
    },
    {
      label: t('Upstream cost'),
      value: props.summary.cost_available
        ? formatBillingCurrencyFromUSD(props.summary.cost_usd)
        : '-',
      icon: ReceiptText,
    },
    {
      label: t('Profit'),
      value: props.summary.profit_available
        ? formatBillingCurrencyFromUSD(props.summary.profit_usd)
        : '-',
      icon: BadgeDollarSign,
      tone: props.summary.profit_available ? profitTone : undefined,
    },
    {
      label: t('Profit margin'),
      value: props.summary.margin_available
        ? `${(props.summary.margin * 100).toFixed(2)}%`
        : '-',
      icon: CirclePercent,
      tone: props.summary.margin_available ? profitTone : undefined,
    },
  ]

  return (
    <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
      {metrics.map((metric) => (
        <Card key={metric.label} size='sm' className='rounded-lg shadow-xs'>
          <CardContent className='flex min-h-20 items-center justify-between gap-4'>
            <div className='min-w-0'>
              <p className='text-muted-foreground truncate text-xs'>
                {metric.label}
              </p>
              <p
                className={cn(
                  'mt-1 truncate text-xl font-semibold tabular-nums',
                  metric.tone
                )}
              >
                {metric.value}
              </p>
            </div>
            <span className='bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-md'>
              <metric.icon className='size-4' aria-hidden='true' />
            </span>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

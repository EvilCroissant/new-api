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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Card, CardContent } from '@/components/ui/card'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { cn } from '@/lib/utils'

import type { ChannelProfitSummary } from '../types'

type Metric = {
  id: string
  label: string
  value: string
  icon: LucideIcon
  iconBg: string
  iconColor: string
  tone?: string
}

type ProfitSummaryCardsProps = {
  summary: ChannelProfitSummary
}

export function ProfitSummaryCards({ summary }: ProfitSummaryCardsProps) {
  const { t } = useTranslation()

  const metrics = useMemo<Metric[]>(() => {
    const profitTone =
      summary.profit_available && summary.profit_usd < 0
        ? 'text-destructive'
        : 'text-emerald-600 dark:text-emerald-400'

    return [
      {
        id: 'revenue',
        label: t('Downstream revenue'),
        value: formatBillingCurrencyFromUSD(summary.revenue_usd),
        icon: TrendingUp,
        iconBg: 'bg-emerald-500/10 dark:bg-emerald-500/15',
        iconColor: 'text-emerald-600 dark:text-emerald-400',
      },
      {
        id: 'cost',
        label: t('Upstream cost'),
        value: summary.cost_available
          ? formatBillingCurrencyFromUSD(summary.cost_usd)
          : '-',
        icon: ReceiptText,
        iconBg: 'bg-amber-500/10 dark:bg-amber-500/15',
        iconColor: 'text-amber-600 dark:text-amber-400',
      },
      {
        id: 'profit',
        label: t('Net profit'),
        value: summary.profit_available
          ? formatBillingCurrencyFromUSD(summary.profit_usd)
          : '-',
        icon: BadgeDollarSign,
        iconBg: 'bg-blue-500/10 dark:bg-blue-500/15',
        iconColor: 'text-blue-600 dark:text-blue-400',
        tone: summary.profit_available ? profitTone : undefined,
      },
      {
        id: 'margin',
        label: t('Profit margin'),
        value: summary.margin_available
          ? `${(summary.margin * 100).toFixed(2)}%`
          : '-',
        icon: CirclePercent,
        iconBg: 'bg-indigo-500/10 dark:bg-indigo-500/15',
        iconColor: 'text-indigo-600 dark:text-indigo-400',
        tone: summary.margin_available ? profitTone : undefined,
      },
    ]
  }, [summary, t])

  return (
    <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
      {metrics.map((metric) => (
        <Card
          key={metric.id}
          size='sm'
          className='bg-card hover:border-foreground/15 rounded-xl border shadow-xs transition-all duration-200 hover:shadow-md'
        >
          <CardContent className='flex min-h-20 items-center justify-between gap-4 p-4'>
            <div className='min-w-0'>
              <p className='text-muted-foreground truncate text-xs font-medium'>
                {metric.label}
              </p>
              <p
                className={cn(
                  'mt-1 truncate text-2xl font-bold tabular-nums tracking-tight text-foreground',
                  metric.tone
                )}
              >
                {metric.value}
              </p>
            </div>
            <span
              className={cn(
                'flex size-10 shrink-0 items-center justify-center rounded-xl transition-colors',
                metric.iconBg,
                metric.iconColor
              )}
            >
              <metric.icon className='size-5' aria-hidden='true' />
            </span>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

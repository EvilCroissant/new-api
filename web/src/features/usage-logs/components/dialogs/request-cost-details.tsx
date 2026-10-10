import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'

import type { RequestUpstreamCost } from '../../types'
import { DetailRow, DetailSection } from './log-detail-layout'

export function RequestCostDetails(props: { cost?: RequestUpstreamCost }) {
  const { t, i18n } = useTranslation()
  const currencyOptions = {
    digitsLarge: 6,
    digitsSmall: 6,
    abbreviate: false,
    locale: toIntlLocale(i18n.resolvedLanguage || i18n.language),
  }
  if (!props.cost) return null
  const cost = props.cost
  let status = t('Unknown')
  if (cost.status === 'matched') status = t('Recorded cost')
  if (cost.status === 'estimated') status = t('Estimated cost')
  const reasons: Record<string, string> = {
    monitoring_disabled: t('Profit monitoring is disabled'),
    configuration_unavailable: t('Cost configuration unavailable'),
    channel_changed: t('Cost channel does not match'),
    ratio_unavailable: t('Upstream ratio unavailable or expired'),
    pricing_unavailable: t('Upstream pricing unavailable'),
    fixed_cost_missing: t('Fixed request cost is missing'),
    usage_unsupported: t('Usage cannot be estimated'),
    pricing_unsupported: t(
      'Upstream pricing requires unavailable request facts'
    ),
    invalid_usage: t('Invalid usage for cost estimation'),
    invalid_cost: t('Invalid upstream cost'),
    recording_failed: t('Cost record could not be saved'),
  }
  const sources: Record<string, string> = {
    upstream_pricing: t('Upstream prices and usage'),
    local_ratio_fallback: t('Local charge scaled by ratios (estimate)'),
    upstream_ratio: t('Upstream group ratio'),
    fixed_request: t('Fixed request cost (USD)'),
    upstream_log: t('Upstream consumption log'),
  }
  return (
    <DetailSection label={t('Per-request upstream cost')}>
      <DetailRow label={t('Cost status')} value={status} />
      {cost.reason && (
        <DetailRow
          label={t('Reason')}
          value={reasons[cost.reason] || cost.reason}
        />
      )}
      {cost.source && (
        <DetailRow
          label={t('Cost basis')}
          value={sources[cost.source] || cost.source}
        />
      )}
      {cost.status !== 'unknown' && cost.cost_usd != null && (
        <DetailRow
          label={t('Upstream cost')}
          value={formatBillingCurrencyFromUSD(cost.cost_usd, currencyOptions)}
        />
      )}
      {cost.status === 'matched' && cost.estimated_usd != null && (
        <DetailRow
          label={t('Original estimate')}
          value={formatBillingCurrencyFromUSD(
            cost.estimated_usd,
            currencyOptions
          )}
        />
      )}
      {cost.cost_factor != null && (
        <DetailRow
          label={t('Purchase cost factor')}
          value={String(cost.cost_factor)}
        />
      )}
      {cost.upstream_ratio != null && (
        <DetailRow
          label={t('Upstream group ratio')}
          value={String(cost.upstream_ratio)}
        />
      )}
    </DetailSection>
  )
}

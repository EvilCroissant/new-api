import { useTranslation } from 'react-i18next'

import type { RequestParameterSnapshot } from '../../types'
import { DetailRow, DetailSection } from './log-detail-layout'

export function RequestParameterDetails(props: {
  snapshot?: RequestParameterSnapshot
}) {
  const { t } = useTranslation()
  if (!props.snapshot) return null

  const parameters = Object.entries(props.snapshot.parameters ?? {}).sort(
    ([left], [right]) => left.localeCompare(right)
  )
  const content = Object.entries(props.snapshot.content ?? {}).sort(
    ([left], [right]) => left.localeCompare(right)
  )

  return (
    <DetailSection label={t('Request parameters')}>
      {parameters.map(([key, value]) => (
        <DetailRow key={key} label={key} value={JSON.stringify(value)} mono />
      ))}
      {content.length > 0 && (
        <div className='border-border/50 mt-2 border-t pt-2'>
          <p className='text-muted-foreground mb-1 text-xs font-medium'>
            {t('Content summary')}
          </p>
          {content.map(([key, value]) => (
            <DetailRow
              key={key}
              label={key}
              value={JSON.stringify(value)}
              mono
            />
          ))}
        </div>
      )}
      {props.snapshot.truncated && (
        <p className='text-muted-foreground text-xs'>
          {t('Summary truncated')}
        </p>
      )}
      {props.snapshot.omitted && (
        <p className='text-muted-foreground text-xs'>
          {t('Some fields omitted')}
        </p>
      )}
    </DetailSection>
  )
}

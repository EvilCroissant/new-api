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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

export type UpstreamManagementTab = 'accounts' | 'profit'

export type UpstreamManagementPageProps = {
  onTabChange?: (tab: UpstreamManagementTab) => void
}

type UpstreamManagementLayoutProps = UpstreamManagementPageProps & {
  tab: UpstreamManagementTab
  actions?: ReactNode
  children: ReactNode
}

export function UpstreamManagementLayout(props: UpstreamManagementLayoutProps) {
  const { t } = useTranslation()

  return (
    <Tabs
      value={props.tab}
      onValueChange={(tab) => {
        if (tab === 'accounts' || tab === 'profit') props.onTabChange?.(tab)
      }}
      className='min-h-0 flex-1 gap-0'
    >
      <SectionPageLayout stackActionsOnMobile>
        <SectionPageLayout.Title>
          {t('Upstream management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>{props.actions}</SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <TabsList
            aria-label={t('Upstream management')}
            className='mb-4 max-w-full flex-wrap justify-start group-data-horizontal/tabs:h-auto'
          >
            <TabsTrigger value='accounts'>
              {t('Account monitoring')}
            </TabsTrigger>
            <TabsTrigger value='profit'>{t('Channel profit')}</TabsTrigger>
          </TabsList>
          <TabsContent value={props.tab}>{props.children}</TabsContent>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    </Tabs>
  )
}

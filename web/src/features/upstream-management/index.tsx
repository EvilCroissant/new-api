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
import { lazy, Suspense } from 'react'

import { LoadingState } from '@/components/loading-state'

import {
  UpstreamManagementLayout,
  type UpstreamManagementPageProps,
  type UpstreamManagementTab,
} from './components/upstream-management-layout'

const ProfitPage = lazy(() =>
  import('@/features/channel-profit').then((module) => ({
    default: module.ChannelProfit,
  }))
)

export function UpstreamManagementPage(
  props: UpstreamManagementPageProps & { tab: UpstreamManagementTab }
) {
  return (
    <Suspense
      fallback={
        <UpstreamManagementLayout
          tab={props.tab}
          onTabChange={props.onTabChange}
        >
          <LoadingState />
        </UpstreamManagementLayout>
      }
    >
      <ProfitPage onTabChange={props.onTabChange} />
    </Suspense>
  )
}

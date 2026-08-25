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

import { z } from 'zod'

export const upstreamMonitorFormSchema = z
  .object({
    name: z.string().trim().max(100, 'Monitor name is too long').optional(),
    base_url: z
      .string()
      .trim()
      .min(1, 'Upstream URL is required')
      .url('Enter a valid URL'),
    provider: z.enum(['newapi', 'sub2api']).optional(),
    new_api_user_id: z.coerce.number().int().positive().optional(),
    access_token: z.string().trim().min(1, 'Credential is required'),
    refresh_token: z.string().trim().optional(),
  })
  .superRefine((value, context) => {
    if (!value.provider) {
      context.addIssue({
        code: 'custom',
        path: ['provider'],
        message: 'Detect the site or select its type manually',
      })
    }
    if (value.provider === 'newapi' && !value.new_api_user_id) {
      context.addIssue({
        code: 'custom',
        path: ['new_api_user_id'],
        message: 'New API user ID is required',
      })
    }
    if (value.provider === 'sub2api' && !value.refresh_token) {
      context.addIssue({
        code: 'custom',
        path: ['refresh_token'],
        message: 'Refresh token is required',
      })
    }
  })

export type UpstreamMonitorFormInput = z.input<typeof upstreamMonitorFormSchema>
export type UpstreamMonitorFormValues = z.output<
  typeof upstreamMonitorFormSchema
>

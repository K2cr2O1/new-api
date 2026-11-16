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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export type ModerationMode = 'auto' | 'true' | 'false'

export type ModerationSettings = {
  enabled: ModerationMode
  effective_enabled: boolean
  credentials_present: boolean
  blocklist: string
  blocklist_is_default: boolean
}

export type ModerationLog = {
  id: number
  created_at: number
  user_id: number
  token_id: number
  model_name: string
  stage: string
  verdict: string
  reason_code: string
  categories: string
  request_id: string
  provider_request_id: string
  content_hash: string
  excerpt: string
}

type Page<T> = {
  page: number
  page_size: number
  total: number
  items: T[]
}

type Envelope<T> = {
  success: boolean
  message: string
  data: T
}

export async function getModerationSettings(): Promise<ModerationSettings> {
  const response = await api.get<Envelope<ModerationSettings>>(
    '/api/moderation/settings'
  )
  return requireServerSuccess(response.data).data
}

export async function updateModerationSettings(input: {
  enabled: ModerationMode
  blocklist: string
}): Promise<void> {
  const response = await api.put<Envelope<unknown>>(
    '/api/moderation/settings',
    input
  )
  requireServerSuccess(response.data)
}

export async function getModerationLogs(): Promise<Page<ModerationLog>> {
  const response = await api.get<Envelope<Page<ModerationLog>>>(
    '/api/moderation/logs',
    { params: { p: 1, page_size: 20 } }
  )
  return requireServerSuccess(response.data).data
}

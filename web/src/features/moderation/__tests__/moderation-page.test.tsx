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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ModerationPage } from '../index'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('saves the moderation mode and blocklist and lists a log row', async () => {
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, message: '', data: {} },
  })
  vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (String(url).includes('/logs')) {
      return {
        data: {
          success: true,
          message: '',
          data: {
            page: 1,
            page_size: 20,
            total: 1,
            items: [
              {
                id: 1,
                created_at: 1_700_000_000,
                user_id: 2,
                token_id: 3,
                model_name: 'gpt-test',
                stage: 'prompt',
                verdict: 'block',
                reason_code: 'restricted_content',
                categories: '["child_sexual_content"]',
                request_id: 'req-1',
                provider_request_id: 'REQ_1',
                content_hash: 'abc',
                excerpt: 'short excerpt',
              },
            ],
          },
        },
      }
    }
    return {
      data: {
        success: true,
        message: '',
        data: {
          enabled: 'auto',
          effective_enabled: true,
          credentials_present: true,
          blocklist: 'child_sexual_content|child porn\n',
          blocklist_is_default: true,
        },
      },
    }
  })

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ModerationPage />
    </QueryClientProvider>
  )

  expect(
    await screen.findByText(
      'Prompt Sift checks user text before a generation request is sent upstream. Text outputs are checked against the blocklist. Image and video pixels are not scanned. A review verdict is rejected immediately.'
    )
  ).toBeInTheDocument()
  expect(await screen.findByText('short excerpt')).toBeInTheDocument()
  expect(
    screen.getByText('Waffo merchant credentials are present')
  ).toBeInTheDocument()
  expect(screen.queryByText('Waffo review will pass')).not.toBeInTheDocument()

  const blocklist = screen.getByLabelText('Output blocklist')
  await userEvent.clear(blocklist)
  await userEvent.type(blocklist, 'custom|example phrase')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() => {
    expect(put).toHaveBeenCalledWith('/api/moderation/settings', {
      enabled: 'auto',
      blocklist: 'custom|example phrase',
    })
  })
})

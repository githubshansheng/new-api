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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { GlobalSettingsCard } from '../global-settings-card'

const defaults = {
  global: {
    pass_through_request_enabled: false,
    thinking_model_blacklist: '[]',
    chat_completions_to_responses_policy: '{}',
  },
  general_setting: {
    ping_interval_enabled: false,
    ping_interval_seconds: 10,
  },
}

function Fixture() {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: { mutations: { retry: false } },
      })
  )
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <div ref={setActions} />
        <GlobalSettingsCard defaultValues={defaults} />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
}

describe('model mapping visibility settings', () => {
  it('defaults to visible and saves independent switches and whitelists without changing other settings', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true, message: '' } })
    const user = userEvent.setup()
    render(<Fixture />)
    const upstream = screen.getByRole('switch', {
      name: 'Upstream model mapping visible',
    })
    const site = screen.getByRole('switch', {
      name: 'Site model mapping visible',
    })
    expect(upstream).toBeChecked()
    expect(site).toBeChecked()
    await user.click(upstream)
    expect(site).toBeChecked()
    await user.click(site)
    fireEvent.input(
      screen.getByRole('textbox', {
        name: 'Upstream model visibility whitelist',
      }),
      { target: { value: '["alias-a"]' } }
    )
    fireEvent.input(
      screen.getByRole('textbox', { name: 'Site model visibility whitelist' }),
      { target: { value: '["alias-b","alias-c"]' } }
    )
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(4))
    expect(put.mock.calls.map(([url, body]) => [url, body])).toEqual([
      [
        '/api/option/',
        { key: 'global.upstream_model_mapping_visible', value: false },
      ],
      [
        '/api/option/',
        { key: 'global.site_model_mapping_visible', value: false },
      ],
      [
        '/api/option/',
        {
          key: 'global.upstream_model_mapping_whitelist',
          value: '["alias-a"]',
        },
      ],
      [
        '/api/option/',
        {
          key: 'global.site_model_mapping_whitelist',
          value: '["alias-b","alias-c"]',
        },
      ],
    ])
  })

  it.each(['{}', '[""]', '[7]', 'null'])(
    'rejects invalid whitelist %s without sending settings',
    async (value) => {
      const put = vi
        .spyOn(api, 'put')
        .mockResolvedValue({ data: { success: true } })
      const user = userEvent.setup()
      render(<Fixture />)
      fireEvent.input(
        screen.getByRole('textbox', {
          name: 'Upstream model visibility whitelist',
        }),
        { target: { value } }
      )
      await user.click(screen.getByRole('button', { name: 'Save Changes' }))
      expect(
        await screen.findByText(
          'Model whitelist must be a JSON array of non-empty strings'
        )
      ).toBeVisible()
      expect(put).not.toHaveBeenCalled()
    }
  )
})

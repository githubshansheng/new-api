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
import { createInstance } from 'i18next'
import { describe, expect, test } from 'vitest'

import ja from '../locales/ja.json'
import vi from '../locales/vi.json'

describe('translated message interpolation', () => {
  test.each([
    {
      label: 'Japanese JSON errors include the supplied filename',
      language: 'ja',
      key: 'Failed to parse JSON file: {{name}}',
      values: { name: 'account.json' },
      expected: 'JSONファイルの解析に失敗しました：account.json',
    },
    {
      label: 'Japanese model pull errors include the supplied reason',
      language: 'ja',
      key: 'Model pull failed: {{msg}}',
      values: { msg: 'connection refused' },
      expected: 'モデルのプルに失敗しました：connection refused',
    },
    {
      label: 'Japanese service account messages include the supplied count',
      language: 'ja',
      key: 'Parsed {{count}} service account file(s)',
      values: { count: 3 },
      expected: '3 件のサービスアカウントファイルを解析しました',
    },
    {
      label:
        'Vietnamese trust level labels display LinuxDO without interpolation',
      language: 'vi',
      key: 'Minimum LinuxDO trust level required',
      values: {},
      expected: 'Yêu cầu mức độ tin cậy LinuxDO tối thiểu',
    },
  ])('$label', async (scenario) => {
    const i18n = createInstance()
    await i18n.init({
      lng: scenario.language,
      fallbackLng: false,
      resources: { ja, vi },
      interpolation: { escapeValue: false },
    })

    expect(i18n.t(scenario.key, scenario.values)).toBe(scenario.expected)
  })
})

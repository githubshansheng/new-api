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
import { describe, expect, test } from 'vitest'

import { renderAuditContent } from '../format'

const interpolate = (key: string, values?: Record<string, unknown>) =>
  Object.entries(values ?? {}).reduce(
    (text, [name, value]) => text.replaceAll(`{{${name}}}`, String(value)),
    key
  )

describe('limited quota reclaim audit formatting', () => {
  const cases: Array<{
    action: string
    params: Record<string, string | number | boolean | string[]>
    expected: string
  }> = [
    {
      action: 'redemption.reclaim.enable',
      params: { enabled_count: 3, manual_review_count: 1 },
      expected:
        'Enabled limited quota reclaim for 3 redemption codes (1 awaiting manual review)',
    },
    {
      action: 'redemption.reclaim.review_retry',
      params: { redemption_id: 7, updated_count: 2 },
      expected:
        'Retried automatic limited quota review for redemption 7 (2 records updated)',
    },
    {
      action: 'redemption.reclaim.review_resolve',
      params: { redemption_id: 7, remaining_quota: 500_000 },
      expected:
        'Manually resolved limited quota review for redemption 7 with 500000 remaining quota',
    },
  ]

  test.each(cases)(
    'renders $action with structured parameters',
    ({ action, params, expected }) => {
      expect(renderAuditContent({ op: { action, params } }, interpolate)).toBe(
        expected
      )
    }
  )
})

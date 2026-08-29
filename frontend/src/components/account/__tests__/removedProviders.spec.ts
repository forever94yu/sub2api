import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const removedProviderPattern = /kimi|moonshot|zhipu|deepseek|\bglm(?:[-_./]|$)/i

describe('removed provider account surfaces', () => {
  it('does not expose removed providers in account create or edit flows', () => {
    for (const file of [
      'src/components/account/CreateAccountModal.vue',
      'src/components/account/EditAccountModal.vue',
      'src/components/account/AccountUsageCell.vue',
      'src/components/account/credentialsBuilder.ts',
      'src/composables/useModelWhitelist.ts'
    ]) {
      expect(readFileSync(resolve(file), 'utf8'), file).not.toMatch(removedProviderPattern)
    }
  })

  it('does not ship dedicated provider APIs or account components', () => {
    for (const file of [
      'src/api/admin/cnProviders.ts',
      'src/components/account/CnBaseUrlPresets.vue',
      'src/components/account/CNProviderQuotaCell.vue',
      'src/components/account/CNProviderBalanceCell.vue'
    ]) {
      expect(existsSync(resolve(file)), file).toBe(false)
    }
  })
})

import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const headerSource = readFileSync(resolve(dir, '../AppHeader.vue'), 'utf8')
const repositoryConstantsPath = resolve(dir, '../../../constants/repository.ts')
const repositoryConstantsSource = existsSync(repositoryConstantsPath)
  ? readFileSync(repositoryConstantsPath, 'utf8')
  : ''

describe('repository links', () => {
  it('keeps the profile dropdown GitHub link on the owner repository', () => {
    expect(repositoryConstantsSource).toContain(
      "export const OWNER_REPOSITORY_URL = 'https://github.com/forever94yu/sub2api'",
    )
    expect(headerSource).toContain("import { OWNER_REPOSITORY_URL } from '@/constants/repository'")
    expect(headerSource).toContain(':href="OWNER_REPOSITORY_URL"')
  })
})

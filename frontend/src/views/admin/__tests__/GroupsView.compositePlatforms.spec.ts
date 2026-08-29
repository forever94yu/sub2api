import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('GroupsView Composite route options', () => {
  it('excludes removed providers from route targets', () => {
    const source = readFileSync(resolve('src/views/admin/GroupsView.vue'), 'utf8')
    const options = source.slice(
      source.indexOf('const compositeRoutePlatformOptions'),
      source.indexOf('const compositeRouteEndpointOptions')
    )

    expect(options).not.toMatch(/kimi|zhipu|deepseek/i)
  })
})

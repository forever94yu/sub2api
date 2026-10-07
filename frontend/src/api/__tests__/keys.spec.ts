import { beforeEach, describe, expect, it, vi } from 'vitest'
import { keysAPI } from '../keys'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { post } }))

describe('API key creation expiration', () => {
  beforeEach(() => post.mockReset().mockResolvedValue({ data: {} }))

  it('sends the exact RFC3339 expiration selected by the user', async () => {
    await keysAPI.create('hour-key', 1, undefined, [], [], 0, undefined, undefined, '2099-10-07T05:15:00.000Z')
    expect(post).toHaveBeenCalledWith('/keys', {
      name: 'hour-key', group_id: 1, expires_at: '2099-10-07T05:15:00.000Z',
    })
  })

  it('preserves legacy expiration in days', async () => {
    await keysAPI.create('legacy', 1, undefined, [], [], 0, 7)
    expect(post).toHaveBeenCalledWith('/keys', { name: 'legacy', group_id: 1, expires_in_days: 7 })
  })
})

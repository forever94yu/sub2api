import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const styleSource = () => readFileSync(resolve(__dirname, '../../../style.css'), 'utf8')

function cssRuleBody(selector: string) {
  const source = styleSource()
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const match = source.match(new RegExp(`${escapedSelector}\\s*\\{([\\s\\S]*?)\\n\\s*\\}`))
  return match?.[1] ?? ''
}

describe('BaseDialog scroll paint stability', () => {
  it('keeps the full-screen modal overlay free of backdrop filters', () => {
    const overlayRule = cssRuleBody('.modal-overlay')

    expect(overlayRule).not.toContain('backdrop-blur')
    expect(overlayRule).not.toContain('backdrop-filter')
  })

  it('contains wheel scrolling inside the modal body', () => {
    const modalBodyRule = cssRuleBody('.modal-body')

    expect(modalBodyRule).toContain('overscroll-behavior: contain')
  })
})

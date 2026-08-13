// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'
import { writeTextToClipboard } from './clipboard'

const setClipboard = (clipboard: Pick<Clipboard, 'writeText'> | undefined) => {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: clipboard,
  })
}

const setExecCommand = (implementation: (command: string) => boolean) => {
  Object.defineProperty(document, 'execCommand', {
    configurable: true,
    value: vi.fn(implementation),
  })
}

afterEach(() => {
  vi.restoreAllMocks()
  setClipboard(undefined)
  document.getSelection()?.removeAllRanges()
  document.body.replaceChildren()
  Object.defineProperty(document, 'execCommand', {
    configurable: true,
    value: undefined,
  })
})

describe('writeTextToClipboard', () => {
  it('uses the Clipboard API when it is available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    const execCommand = vi.fn(() => true)
    setClipboard({ writeText })
    setExecCommand(execCommand)

    await writeTextToClipboard('api-key')

    expect(writeText).toHaveBeenCalledWith('api-key')
    expect(execCommand).not.toHaveBeenCalled()
  })

  it('uses the fallback when the Clipboard API is unavailable', async () => {
    setClipboard(undefined)
    setExecCommand((command) => {
      const textarea = document.querySelector('textarea')
      expect(command).toBe('copy')
      expect(textarea?.value).toBe('http-api-key')
      return true
    })

    await writeTextToClipboard('http-api-key')

    expect(document.querySelector('textarea')).toBeNull()
  })

  it('uses the fallback when the Clipboard API rejects', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('permission denied'))
    setClipboard({ writeText })
    setExecCommand(() => true)

    await expect(writeTextToClipboard('api-key')).resolves.toBeUndefined()
    expect(document.execCommand).toHaveBeenCalledWith('copy')
  })

  it('restores focus and selection after fallback copying', async () => {
    const input = document.createElement('input')
    const selectedText = document.createTextNode('selected text')
    const range = document.createRange()
    document.body.append(input, selectedText)
    input.focus()
    range.selectNodeContents(selectedText)
    document.getSelection()?.removeAllRanges()
    document.getSelection()?.addRange(range)
    setClipboard(undefined)
    setExecCommand(() => true)

    await writeTextToClipboard('api-key')

    expect(document.activeElement).toBe(input)
    expect(document.getSelection()?.toString()).toBe('selected text')
  })

  it('cleans up and rejects when the fallback cannot copy', async () => {
    setClipboard(undefined)
    setExecCommand(() => false)

    await expect(writeTextToClipboard('api-key')).rejects.toThrow('Failed to copy text to clipboard')
    expect(document.querySelector('textarea')).toBeNull()
  })
})

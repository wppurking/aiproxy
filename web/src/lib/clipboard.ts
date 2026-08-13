const COPY_ERROR_MESSAGE = 'Failed to copy text to clipboard'

const copyTextWithExecCommand = (text: string): void => {
  if (typeof document === 'undefined' || !document.body) {
    throw new Error(COPY_ERROR_MESSAGE)
  }

  const activeElement = document.activeElement
  const selection = document.getSelection()
  const ranges = selection
    ? Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index).cloneRange())
    : []
  const textarea = document.createElement('textarea')

  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.top = '0'
  textarea.style.left = '0'
  textarea.style.width = '1px'
  textarea.style.height = '1px'
  textarea.style.opacity = '0'
  textarea.style.pointerEvents = 'none'

  document.body.appendChild(textarea)

  try {
    textarea.select()
    textarea.setSelectionRange(0, text.length)

    if (typeof document.execCommand !== 'function' || !document.execCommand('copy')) {
      throw new Error(COPY_ERROR_MESSAGE)
    }
  } finally {
    textarea.remove()

    if (activeElement instanceof HTMLElement) {
      activeElement.focus()
    }

    if (selection) {
      selection.removeAllRanges()
      ranges.forEach((range) => selection.addRange(range))
    }
  }
}

export const writeTextToClipboard = async (text: string): Promise<void> => {
  const clipboard = typeof navigator === 'undefined' ? undefined : navigator.clipboard

  if (clipboard?.writeText) {
    try {
      await clipboard.writeText(text)
      return
    } catch {
      copyTextWithExecCommand(text)
      return
    }
  }

  copyTextWithExecCommand(text)
}

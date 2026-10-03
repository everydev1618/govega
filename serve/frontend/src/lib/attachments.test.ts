import { describe, it, expect } from 'vitest'
import { partitionDroppedFiles, buildAttachmentNote, composeMessage, dragCarriesFiles } from './attachments'
import type { UploadedFile } from './types'

// Node's File is available under vitest's default environment; these are
// metadata-only fixtures, nothing reads the bytes.
function f(name: string, type: string): File {
  return new File(['x'], name, { type })
}

const md: UploadedFile = {
  name: 'etienne-voice-prompt.md',
  path: 'uploads/etienne-voice-prompt.md',
  content_type: 'text/markdown',
  size: 42,
}
const csv: UploadedFile = {
  name: 'q3.csv',
  path: 'uploads/q3.csv',
  content_type: 'text/csv',
  size: 99,
}

describe('partitionDroppedFiles', () => {
  it('sends images inline and everything else to the workspace', () => {
    const { images, uploads } = partitionDroppedFiles([
      f('shot.png', 'image/png'),
      f('notes.md', 'text/markdown'),
      f('data.csv', 'text/csv'),
    ])
    expect(images.map(i => i.name)).toEqual(['shot.png'])
    expect(uploads.map(u => u.name)).toEqual(['notes.md', 'data.csv'])
  })

  it('uploads SVGs rather than inlining them — the vision API rejects them', () => {
    const { images, uploads } = partitionDroppedFiles([f('logo.svg', 'image/svg+xml')])
    expect(images).toEqual([])
    expect(uploads.map(u => u.name)).toEqual(['logo.svg'])
  })

  it('uploads a file the OS gave no MIME type for', () => {
    const { images, uploads } = partitionDroppedFiles([f('Makefile', '')])
    expect(uploads.map(u => u.name)).toEqual(['Makefile'])
    expect(images).toEqual([])
  })
})

describe('buildAttachmentNote', () => {
  it('is empty when nothing was attached', () => {
    expect(buildAttachmentNote([])).toBe('')
  })

  it('names the workspace path so the agent can read the file', () => {
    const note = buildAttachmentNote([md])
    expect(note).toContain('uploads/etienne-voice-prompt.md')
    expect(note).toContain('read_file')
  })

  it('lists every attachment', () => {
    const note = buildAttachmentNote([md, csv])
    expect(note).toContain('uploads/etienne-voice-prompt.md')
    expect(note).toContain('uploads/q3.csv')
  })
})

describe('composeMessage', () => {
  it('leaves a plain message untouched', () => {
    expect(composeMessage('hello', [])).toBe('hello')
  })

  it('appends the note below the user text', () => {
    const out = composeMessage('summarise this', [md])
    expect(out.startsWith('summarise this')).toBe(true)
    expect(out).toContain('uploads/etienne-voice-prompt.md')
  })

  it('still produces a usable message when only files were dropped', () => {
    const out = composeMessage('', [md])
    expect(out).toContain('uploads/etienne-voice-prompt.md')
    expect(out.trim().length).toBeGreaterThan(0)
  })
})

describe('dragCarriesFiles', () => {
  it('recognises a file drag from the OS', () => {
    expect(dragCarriesFiles(['Files'])).toBe(true)
  })

  it('ignores a text selection being dragged within the page', () => {
    expect(dragCarriesFiles(['text/plain', 'text/html'])).toBe(false)
  })

  it('tolerates a missing types list', () => {
    expect(dragCarriesFiles(undefined)).toBe(false)
  })
})

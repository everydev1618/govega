import type { ChatImage, UploadedFile } from './types'

// Attachment handling for the chat composer.
//
// Two destinations, decided by type. Raster images go inline as vision
// content blocks — the model sees them in the turn itself. Everything else
// is uploaded into the workspace and referenced by path, because the LLM
// wire format has nowhere to put a .md or a .csv, and because a file the
// agent can re-open later beats one that existed for a single turn.

// inlineableImage reports whether a dropped file can ride along as a vision
// block. SVG is excluded deliberately: it has an image/* type but the
// vision API rejects it, so it belongs on the upload path.
export function inlineableImage(file: File): boolean {
  return file.type.startsWith('image/') && file.type !== 'image/svg+xml'
}

// dragCarriesFiles distinguishes an OS file drag from dragging a text
// selection around inside the page — without it the drop overlay flashes
// up every time someone drags a word across the composer.
export function dragCarriesFiles(types: readonly string[] | undefined): boolean {
  if (!types) return false
  return Array.from(types).includes('Files')
}

export function partitionDroppedFiles(files: File[]): { images: File[]; uploads: File[] } {
  const images: File[] = []
  const uploads: File[] = []
  for (const f of files) {
    if (inlineableImage(f)) images.push(f)
    else uploads.push(f)
  }
  return { images, uploads }
}

// buildAttachmentNote renders the uploaded files as a block appended to the
// outgoing message. The agent gets workspace-relative paths and is told
// what to do with them; without this the upload would succeed and the
// agent would never learn the file exists.
export function buildAttachmentNote(files: UploadedFile[]): string {
  if (files.length === 0) return ''
  const lines = files.map(f => `- ${f.path}`).join('\n')
  const noun = files.length === 1 ? 'file' : 'files'
  return `[Attached ${noun} — saved in the workspace, open with read_file:]\n${lines}`
}

export function composeMessage(text: string, files: UploadedFile[]): string {
  const note = buildAttachmentNote(files)
  if (!note) return text
  if (!text.trim()) return note
  return `${text}\n\n${note}`
}

// chatImageFromFile reads an image File into the base64 wire shape the
// chat API takes.
export async function chatImageFromFile(file: File): Promise<ChatImage | null> {
  if (!inlineableImage(file)) return null
  const dataURL: string = await new Promise((resolve, reject) => {
    const fr = new FileReader()
    fr.onload = () => resolve(String(fr.result))
    fr.onerror = reject
    fr.readAsDataURL(file)
  })
  const comma = dataURL.indexOf(',')
  if (comma < 0) return null
  return { media_type: file.type, data: dataURL.slice(comma + 1) }
}

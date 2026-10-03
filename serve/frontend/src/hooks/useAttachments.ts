import { useCallback, useState } from 'react'
import { api } from '../lib/api'
import type { UploadedFile } from '../lib/types'

// useAttachments uploads dropped files into the workspace and tracks the
// resulting paths until the message is sent.
//
// The upload happens on drop rather than on send so the user sees the file
// land (and sees it fail) while they are still writing, instead of losing
// a composed message to an upload error.
export function useAttachments() {
  const [files, setFiles] = useState<UploadedFile[]>([])
  const [uploading, setUploading] = useState(0)
  const [error, setError] = useState<string | null>(null)

  const add = useCallback(async (local: File[]) => {
    if (local.length === 0) return
    setError(null)
    setUploading(n => n + local.length)
    for (const f of local) {
      try {
        const up = await api.uploadFile(f)
        setFiles(prev => [...prev, up])
      } catch (err) {
        setError(`${f.name}: ${err instanceof Error ? err.message : 'upload failed'}`)
      } finally {
        setUploading(n => n - 1)
      }
    }
  }, [])

  const remove = useCallback((path: string) => {
    setFiles(prev => prev.filter(f => f.path !== path))
  }, [])

  const clear = useCallback(() => {
    setFiles([])
    setError(null)
  }, [])

  return { files, uploading, error, add, remove, clear }
}

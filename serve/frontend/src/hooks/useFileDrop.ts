import { useCallback, useRef, useState } from 'react'
import { dragCarriesFiles } from '../lib/attachments'

// useFileDrop turns a region into a drop target for OS files.
//
// Spread dropProps onto the element that should accept the drop; dragging
// is true while a file drag is over it, for the overlay. The depth counter
// is what keeps the overlay steady: dragenter/dragleave fire for every
// child element the pointer crosses, so a naive boolean flickers as the
// cursor moves across the composer's buttons.
export function useFileDrop(onFiles: (files: File[]) => void, disabled = false) {
  const [dragging, setDragging] = useState(false)
  const depth = useRef(0)

  const reset = useCallback(() => {
    depth.current = 0
    setDragging(false)
  }, [])

  const onDragEnter = useCallback((e: React.DragEvent) => {
    if (disabled || !dragCarriesFiles(e.dataTransfer?.types)) return
    e.preventDefault()
    depth.current += 1
    setDragging(true)
  }, [disabled])

  const onDragOver = useCallback((e: React.DragEvent) => {
    if (disabled || !dragCarriesFiles(e.dataTransfer?.types)) return
    // Without preventDefault here the browser treats the drop as a
    // navigation and opens the file in the tab instead.
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
  }, [disabled])

  const onDragLeave = useCallback((e: React.DragEvent) => {
    if (disabled || !dragCarriesFiles(e.dataTransfer?.types)) return
    e.preventDefault()
    depth.current -= 1
    if (depth.current <= 0) reset()
  }, [disabled, reset])

  const onDrop = useCallback((e: React.DragEvent) => {
    if (disabled || !dragCarriesFiles(e.dataTransfer?.types)) return
    e.preventDefault()
    reset()
    const files = Array.from(e.dataTransfer.files || [])
    if (files.length) onFiles(files)
  }, [disabled, onFiles, reset])

  return { dragging, dropProps: { onDragEnter, onDragOver, onDragLeave, onDrop } }
}

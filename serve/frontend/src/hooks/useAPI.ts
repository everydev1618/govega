import { useState, useEffect, useCallback } from 'react'

interface UseAPIResult<T> {
  data: T | null
  loading: boolean
  error: Error | null
  refetch: () => void
}

export function useAPI<T>(fetcher: () => Promise<T>, deps: unknown[] = []): UseAPIResult<T> {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)

  const refetch = useCallback(() => {
    setLoading(true)
    setError(null)
    fetcher()
      .then(setData)
      .catch(setError)
      .finally(() => setLoading(false))
  }, deps) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    refetch()
  }, [refetch])

  return { data, loading, error, refetch }
}

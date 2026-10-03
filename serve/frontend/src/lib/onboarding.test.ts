import { describe, it, expect } from 'vitest'
import { shouldPromptForPublicURL, suggestedPublicURL } from './onboarding'
import type { OnboardingStatus } from './types'

const base: OnboardingStatus = {
  completed: false,
  needs_public_url: false,
  public_url: 'http://localhost:8822',
  public_url_source: 'fallback',
}

describe('shouldPromptForPublicURL', () => {
  it('prompts when the server could not work the URL out for itself', () => {
    expect(shouldPromptForPublicURL({ ...base, needs_public_url: true })).toBe(true)
  })

  it('stays quiet once answered or dismissed', () => {
    expect(shouldPromptForPublicURL({ ...base, needs_public_url: true, completed: true })).toBe(false)
  })

  it('stays quiet when the server deduced it', () => {
    expect(
      shouldPromptForPublicURL({
        ...base,
        public_url: 'http://vega.const',
        public_url_source: 'observed',
      }),
    ).toBe(false)
  })

  it('stays quiet while the status is still loading', () => {
    expect(shouldPromptForPublicURL(null)).toBe(false)
  })
})

describe('suggestedPublicURL', () => {
  it('prefers a server-side suggestion', () => {
    const status = { ...base, needs_public_url: true, suggestions: ['http://et-m1:8822'] }
    expect(suggestedPublicURL(status, 'http://localhost:8822')).toBe('http://et-m1:8822')
  })

  it('falls back to the origin this browser used', () => {
    const status = { ...base, needs_public_url: true }
    expect(suggestedPublicURL(status, 'http://vega.const')).toBe('http://vega.const')
  })

  // A localhost origin is the very thing we are trying to replace: offering
  // it back as the answer would just re-save the broken value.
  it('does not suggest a loopback origin', () => {
    const status = { ...base, needs_public_url: true }
    expect(suggestedPublicURL(status, 'http://localhost:8822')).toBe('')
    expect(suggestedPublicURL(status, 'http://127.0.0.1:8822')).toBe('')
    expect(suggestedPublicURL(status, 'http://[::1]:8822')).toBe('')
  })
})

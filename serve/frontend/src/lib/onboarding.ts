import type { OnboardingStatus } from './types'

// First-run onboarding, client half. The rule is that we only ask what the
// server could not work out for itself: a browser reaching the dashboard on a
// real hostname already tells the server its public name, so the question
// appears only on an instance bound beyond loopback that nobody has yet
// reached by any name but localhost.

export function shouldPromptForPublicURL(status: OnboardingStatus | null): boolean {
  if (!status) return false
  return status.needs_public_url && !status.completed
}

const LOOPBACK = /^(localhost|127\.\d+\.\d+\.\d+|::1)$/i

function isLoopbackOrigin(origin: string): boolean {
  try {
    // URL.hostname keeps the brackets on an IPv6 literal; strip them so ::1
    // is recognised.
    const host = new URL(origin).hostname.replace(/^\[|\]$/g, '')
    return LOOPBACK.test(host)
  } catch {
    return false
  }
}

// suggestedPublicURL pre-fills the form: whatever the server proposed (its own
// hostname), else the origin this browser used — unless that is loopback,
// which is the broken value we are here to replace.
export function suggestedPublicURL(status: OnboardingStatus | null, currentOrigin: string): string {
  const fromServer = status?.suggestions?.[0]
  if (fromServer) return fromServer
  if (!currentOrigin || isLoopbackOrigin(currentOrigin)) return ''
  return currentOrigin
}

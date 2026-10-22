const OAUTH_POPUP_FLOW_KEY_PREFIX = 'oauth_popup_flow:'

export type OAuthCallbackMode = 'login' | 'bind' | 'verify'

export interface OAuthModeStorage {
  getItem: (key: string) => string | null
  setItem: (key: string, value: string) => void
  removeItem?: (key: string) => void
}

export function getOAuthSessionStorage(
  owner: { readonly sessionStorage: OAuthModeStorage } | null | undefined
): OAuthModeStorage | null {
  try {
    return owner?.sessionStorage ?? null
  } catch {
    return null
  }
}

export function markOAuthPopup(
  storage: OAuthModeStorage | null | undefined,
  provider: string,
  state: string,
  intent: 'bind' | 'verify'
): boolean {
  if (!storage || !provider || !state) return false
  try {
    const key = `${OAUTH_POPUP_FLOW_KEY_PREFIX}${provider}`
    const marker = JSON.stringify({ state, intent })
    storage.setItem(key, marker)
    return storage.getItem(key) === marker
  } catch {
    return false
  }
}

export function resolveOAuthCallbackMode(
  provider: string,
  state: string,
  context: {
    opener: { closed: boolean } | null | undefined
    storage: OAuthModeStorage | null | undefined
  }
): OAuthCallbackMode {
  if (
    !context.opener ||
    context.opener.closed ||
    !context.storage ||
    !state
  ) {
    return 'login'
  }
  try {
    const raw = context.storage.getItem(
      `${OAUTH_POPUP_FLOW_KEY_PREFIX}${provider}`
    )
    if (!raw) return 'login'
    const marker = JSON.parse(raw) as { state?: unknown; intent?: unknown }
    if (marker.state !== state) return 'login'
    return marker.intent === 'bind' || marker.intent === 'verify'
      ? marker.intent
      : 'login'
  } catch {
    return 'login'
  }
}

import type { GroupPlatform } from '@/types'

export const OPENAI_CC_SWITCH_CODEX_MODEL = 'gpt-5.4'
export const CLAUDE_CC_SWITCH_DEFAULT_MODEL = 'claude-sonnet-4-5-20250929'
export const CLAUDE_CC_SWITCH_HAIKU_MODEL = 'claude-haiku-4-5-20251001'
export const CLAUDE_CC_SWITCH_SONNET_MODEL = 'claude-sonnet-4-5-20250929'
export const CLAUDE_CC_SWITCH_OPUS_MODEL = 'claude-opus-4-5-20251101'
export const KIRO_CC_SWITCH_DEFAULT_MODEL = 'auto-kiro'
export const KIRO_CC_SWITCH_HAIKU_MODEL = 'claude-haiku-4.5'
export const KIRO_CC_SWITCH_SONNET_MODEL = 'claude-sonnet-4.6'
export const KIRO_CC_SWITCH_OPUS_MODEL = 'claude-opus-4.6'

export type CcSwitchClientType = 'claude' | 'gemini'

export interface CcSwitchImportConfig {
  app: string
  endpoint: string
  model?: string
  models?: Record<string, string>
}

export interface CcSwitchImportDeeplinkInput {
  baseUrl: string
  platform?: GroupPlatform | null
  clientType: CcSwitchClientType
  providerName: string
  apiKey: string
  usageScript: string
}

export function normalizeCcSwitchBaseUrl(baseUrl: string): string {
  return (baseUrl || window.location.origin)
    .replace(/\/+$/, '')
    .replace(/\/v1\/messages\/count_tokens\/?$/i, '')
    .replace(/\/v1\/messages\/?$/i, '')
    .replace(/\/v1\/chat\/completions\/?$/i, '')
    .replace(/\/v1\/responses\/?$/i, '')
    .replace(/\/v1beta\/?$/i, '')
    .replace(/\/v1\/?$/i, '')
    .replace(/\/+$/, '')
}

function claudeCcSwitchModels(): Record<string, string> {
  return {
    model: CLAUDE_CC_SWITCH_DEFAULT_MODEL,
    haikuModel: CLAUDE_CC_SWITCH_HAIKU_MODEL,
    sonnetModel: CLAUDE_CC_SWITCH_SONNET_MODEL,
    opusModel: CLAUDE_CC_SWITCH_OPUS_MODEL
  }
}

function kiroCcSwitchModels(): Record<string, string> {
  return {
    model: KIRO_CC_SWITCH_DEFAULT_MODEL,
    haikuModel: KIRO_CC_SWITCH_HAIKU_MODEL,
    sonnetModel: KIRO_CC_SWITCH_SONNET_MODEL,
    opusModel: KIRO_CC_SWITCH_OPUS_MODEL
  }
}

export function resolveCcSwitchImportConfig(
  platform: GroupPlatform | undefined | null,
  clientType: CcSwitchClientType,
  baseUrl: string
): CcSwitchImportConfig {
  const rootBaseUrl = normalizeCcSwitchBaseUrl(baseUrl)

  switch (platform || 'anthropic') {
    case 'antigravity':
      return {
        app: clientType === 'gemini' ? 'gemini' : 'claude',
        endpoint: `${rootBaseUrl}/antigravity`,
        models: clientType === 'claude' ? claudeCcSwitchModels() : undefined
      }
    case 'openai':
      return {
        app: 'codex',
        endpoint: rootBaseUrl,
        model: OPENAI_CC_SWITCH_CODEX_MODEL
      }
    case 'gemini':
      return {
        app: 'gemini',
        endpoint: rootBaseUrl
      }
    case 'kiro':
      return {
        app: 'claude',
        endpoint: `${rootBaseUrl}/kiro`,
        models: kiroCcSwitchModels()
      }
    default:
      return {
        app: 'claude',
        endpoint: rootBaseUrl,
        models: claudeCcSwitchModels()
      }
  }
}

export function buildCcSwitchImportDeeplink(input: CcSwitchImportDeeplinkInput): string {
  const config = resolveCcSwitchImportConfig(input.platform, input.clientType, input.baseUrl)
  const homepage = normalizeCcSwitchBaseUrl(input.baseUrl)
  const entries: [string, string][] = [
    ['resource', 'provider'],
    ['app', config.app],
    ['name', input.providerName],
    ['homepage', homepage],
    ['endpoint', config.endpoint],
    ['apiKey', input.apiKey],
    ['configFormat', 'json'],
    ['usageEnabled', 'true'],
    ['usageScript', btoa(input.usageScript)],
    ['usageAutoInterval', '30']
  ]

  if (config.model) {
    entries.splice(2, 0, ['model', config.model])
  }
  if (config.models) {
    for (const [key, value] of Object.entries(config.models).reverse()) {
      entries.splice(2, 0, [key, value])
    }
  }

  return `ccswitch://v1/import?${new URLSearchParams(entries).toString()}`
}

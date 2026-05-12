import { describe, expect, it } from 'vitest'
import {
  CLAUDE_CC_SWITCH_DEFAULT_MODEL,
  CLAUDE_CC_SWITCH_HAIKU_MODEL,
  CLAUDE_CC_SWITCH_OPUS_MODEL,
  CLAUDE_CC_SWITCH_SONNET_MODEL,
  KIRO_CC_SWITCH_DEFAULT_MODEL,
  OPENAI_CC_SWITCH_CODEX_MODEL,
  buildCcSwitchImportDeeplink,
  normalizeCcSwitchBaseUrl
} from '@/utils/ccswitchImport'
import type { GroupPlatform } from '@/types'

function paramsFromDeeplink(deeplink: string): URLSearchParams {
  const query = deeplink.split('?')[1] || ''
  return new URLSearchParams(query)
}

describe('ccswitchImport utils', () => {
  const baseInput = {
    baseUrl: 'https://api.example.com',
    providerName: 'Sub2API',
    apiKey: 'sk-test',
    usageScript: 'return true'
  }

  it('adds the Codex model parameter for OpenAI imports', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'openai',
        clientType: 'claude'
      })
    )

    expect(params.get('resource')).toBe('provider')
    expect(params.get('app')).toBe('codex')
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
    expect(params.get('model')).toBe(OPENAI_CC_SWITCH_CODEX_MODEL)
    expect(atob(params.get('usageScript') || '')).toBe(baseInput.usageScript)
  })

  it.each([
    ['https://api.example.com/v1', 'https://api.example.com'],
    ['https://api.example.com/v1/messages', 'https://api.example.com'],
    ['https://api.example.com/v1/messages/count_tokens', 'https://api.example.com'],
    ['https://api.example.com/api/v1beta', 'https://api.example.com/api']
  ])('normalizes API endpoint suffixes: %s', (input, expected) => {
    expect(normalizeCcSwitchBaseUrl(input)).toBe(expected)
  })

  it('adds Claude model parameters and strips /v1 for Claude imports', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        baseUrl: 'https://api.example.com/v1',
        platform: 'anthropic',
        clientType: 'claude'
      })
    )

    expect(params.get('app')).toBe('claude')
    expect(params.get('homepage')).toBe('https://api.example.com')
    expect(params.get('endpoint')).toBe('https://api.example.com')
    expect(params.get('model')).toBe(CLAUDE_CC_SWITCH_DEFAULT_MODEL)
    expect(params.get('haikuModel')).toBe(CLAUDE_CC_SWITCH_HAIKU_MODEL)
    expect(params.get('sonnetModel')).toBe(CLAUDE_CC_SWITCH_SONNET_MODEL)
    expect(params.get('opusModel')).toBe(CLAUDE_CC_SWITCH_OPUS_MODEL)
  })

  it('does not add a Claude model parameter for Gemini imports', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'gemini' as GroupPlatform,
        clientType: 'gemini'
      })
    )

    expect(params.get('app')).toBe('gemini')
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
    expect(params.has('model')).toBe(false)
  })

  it('keeps Antigravity imports on the selected client endpoint without a model parameter', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'antigravity',
        clientType: 'gemini'
      })
    )

    expect(params.get('app')).toBe('gemini')
    expect(params.get('endpoint')).toBe(`${baseInput.baseUrl}/antigravity`)
    expect(params.has('model')).toBe(false)
  })

  it('uses the Kiro Claude endpoint and model parameters for Kiro imports', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        baseUrl: 'https://api.example.com/v1',
        platform: 'kiro',
        clientType: 'claude'
      })
    )

    expect(params.get('app')).toBe('claude')
    expect(params.get('endpoint')).toBe('https://api.example.com/kiro')
    expect(params.get('model')).toBe(KIRO_CC_SWITCH_DEFAULT_MODEL)
    expect(params.get('sonnetModel')).toBe('claude-sonnet-4.6')
    expect(params.get('opusModel')).toBe('claude-opus-4.6')
  })
})

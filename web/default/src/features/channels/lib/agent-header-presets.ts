/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export const AGENT_HEADER_DEFAULTS = {
  // Verified against the public client packages on 2026-11-27. Saved channels retain their own values.
  codex: {
    'User-Agent': 'codex_cli_rs/0.172.0 (Windows 10.0; x86_64) WindowsTerminal',
    originator: 'codex_cli_rs',
    version: '0.172.0',
  },
  claude: {
    'User-Agent': 'claude-cli/2.1.332 (external, cli)',
    'x-app': 'cli',
    'anthropic-version': '2023-06-01',
    'anthropic-beta': 'claude-code-20250219',
  },
} as const

export function parseAgentHeaderDefaults(
  value: string | undefined
): Record<string, string> | null {
  try {
    const parsed: unknown = JSON.parse(value || '{}')
    if (
      parsed &&
      typeof parsed === 'object' &&
      !Array.isArray(parsed) &&
      Object.values(parsed).every(
        (item) => typeof item === 'string' && item.trim() !== ''
      )
    ) {
      return parsed as Record<string, string>
    }
  } catch {
    // The form reports malformed drafts before saving.
  }
  return null
}

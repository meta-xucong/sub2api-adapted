// @vitest-environment node
import { readFileSync } from 'node:fs'
import { describe, expect, it, vi } from 'vitest'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'

describe('patched frontend dependencies', () => {
  it('pins the official SheetJS archive with verified integrity', () => {
    const lockfile = readFileSync(new URL('../../../pnpm-lock.yaml', import.meta.url), 'utf8')
    const archive = 'https://cdn.sheetjs.com/xlsx-0.20.3/xlsx-0.20.3.tgz'
    const entry = lockfile.split(`  xlsx@${archive}:`)[1]?.split('\n\n')[0]
    expect(entry).toContain(`tarball: ${archive}`)
    expect(entry).toContain('version: 0.20.3')
    expect(entry).toContain('integrity: sha512-oLDq3jw7AcLqKWH2AhCpVTZl8mf6X2YReP+Neh0SJUzV/BdZYjth94tG5toiMB1PPrYtxOCfaoUCkvtuH+3AJA==')
  })

  it('rejects carriage returns in SSR attribute names (GHSA-g2v6-rqmx-r4w6)', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      const html = await renderToString(createSSRApp({
        render: () => h('input', {
          'x\rautofocus\ronfocus': 'alert(1)',
          title: 'safe'
        })
      }))
      expect(html).toContain('title="safe"')
      expect(html).not.toContain('\r')
      expect(html).not.toContain('autofocus')
      expect(html).not.toContain('onfocus')
    } finally {
      warn.mockRestore()
      error.mockRestore()
    }
  })

  it('preserves the dynamic SheetJS usage-export APIs and XLSX data types', async () => {
    // Exercise the real library, unlike the view test's export mocks.
    const XLSX = await import('xlsx')
    expect(XLSX.version).toBe('0.20.3')
    const headers = ['用户', 'Model', 'Tokens', 'Cost', 'Request ID']
    const rows = [
      ['用户@example.test', 'gpt-image-1', 0, '0.000000', '=1+1'],
      ['second@example.test', 'gpt-image-1', 42, '0.001250', 'req-123']
    ]
    const sheet = XLSX.utils.aoa_to_sheet([headers])
    XLSX.utils.sheet_add_aoa(sheet, rows, { origin: -1 })
    const workbook = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(workbook, sheet, 'Usage')

    const bytes = XLSX.write(workbook, { bookType: 'xlsx', type: 'array' })
    expect(bytes.byteLength).toBeGreaterThan(0)
    const exported = XLSX.read(bytes, { type: 'array' })
    expect(exported.SheetNames).toEqual(['Usage'])
    expect(XLSX.utils.sheet_to_json(exported.Sheets.Usage, { header: 1 })).toEqual([
      headers,
      ...rows
    ])
    expect(exported.Sheets.Usage.E2).toMatchObject({ t: 's', v: '=1+1' })
    expect(exported.Sheets.Usage.E2.f).toBeUndefined()
  })
})

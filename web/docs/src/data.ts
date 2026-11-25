// What data.js, written at generation time, holds: the search index, the
// hover cards and the catalogue strings the islands show.

export interface Entry {
  t: string // title
  k: string // kind
  p: string // node path
  u: string // url
  g?: string // the page it's on
  x?: string // text
}

export interface Card {
  t: string
  k: string
  h: string
  u: string
}

export interface Data {
  search: Entry[]
  cards: Record<string, Card>
  strings: Record<string, string>
}

declare global {
  interface Window {
    __HERON__?: Data
  }
}

export const data = (): Data => window.__HERON__ ?? { search: [], cards: {}, strings: {} }

/** A catalogue string; the key itself when data.js hasn't loaded. */
export const t = (key: string): string => data().strings[key] ?? key

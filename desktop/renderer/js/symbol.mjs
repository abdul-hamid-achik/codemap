/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Symbol-shape normalisation shared by the renderer and the graph builder.
// codemap is honest about nesting: some wrappers (traverse hops, batch frames)
// carry the full symbol record INSIDE a `symbol` key next to a durable
// `selector`, while most reports keep it flat. Views must not care which.

export function symOf(row) {
  if (row === null || row === undefined) return null
  if (typeof row !== 'object') return { symbol: String(row), fqn: String(row) }
  const inner = row.symbol && typeof row.symbol === 'object' ? row.symbol : null
  if (!inner) return row
  const sel = row.selector || inner.selector || null
  return {
    ...inner,
    ...(sel || {}),
    symbol: inner.symbol || inner.fqn || sel?.fqn,
    fqn: inner.fqn || sel?.fqn,
    kind: inner.kind || sel?.kind,
    file: inner.file || sel?.file,
    start_line: inner.start_line ?? sel?.start_line,
    end_line: inner.end_line ?? sel?.end_line,
    signature: inner.signature,
    doc: inner.doc,
    depth: row.depth,
    direction: row.direction,
    edge_type: row.edge_type,
    weight: row.weight,
    provenance: row.provenance,
    confidence: row.confidence,
    confidence_reason: row.confidence_reason,
    parent_selector: row.parent_selector,
    selector: sel,
  }
}

/** True when a row carries enough position info to be opened in the browser. */
export function isSymbolShaped(o) {
  if (!o || typeof o !== 'object') return false
  if (o.start_line !== undefined) return true
  if (o.fqn !== undefined && o.file !== undefined) return true
  if (typeof o.symbol === 'string' && o.file !== undefined) return true
  if (o.selector && o.selector.file !== undefined) return true
  return false
}

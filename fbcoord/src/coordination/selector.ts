export interface NodePreference {
  nodeId: string;
  upstreams: string[];
}

export function selectSharedUpstream(nodes: NodePreference[]): string | null {
  if (nodes.length === 0) {
    return null;
  }
  for (const node of nodes) {
    if (node.upstreams.length === 0) {
      return null;
    }
  }

  const shared = new Set(nodes[0].upstreams);
  for (let i = 1; i < nodes.length; i += 1) {
    const current = new Set(nodes[i].upstreams);
    for (const upstream of Array.from(shared)) {
      if (!current.has(upstream)) {
        shared.delete(upstream);
      }
    }
    if (shared.size === 0) {
      return null;
    }
  }

  let bestUpstream: string | null = null;
  let bestRank = Number.POSITIVE_INFINITY;
  for (const upstream of shared) {
    let rank = 0;
    for (const node of nodes) {
      rank += node.upstreams.indexOf(upstream);
    }
    if (
      bestUpstream === null ||
      rank < bestRank ||
      (rank === bestRank && upstream.localeCompare(bestUpstream) < 0)
    ) {
      bestUpstream = upstream;
      bestRank = rank;
    }
  }

  return bestUpstream;
}

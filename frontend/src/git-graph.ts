export type GraphCommit = { hash: string; parents: string[] }
export function layoutGitGraph(commits: GraphCommit[]) {
 let lanes: string[] = [], width = 1
 const rows = commits.map(commit => {
  const incoming = lanes.includes(commit.hash)
  if (!incoming) lanes.push(commit.hash)
  const before = [...lanes], lane = before.indexOf(commit.hash)
  const after = before.filter(hash => hash !== commit.hash)
  commit.parents.forEach((parent, index) => {
   if (!after.includes(parent)) after.splice(index === 0 ? Math.min(lane, after.length) : after.length, 0, parent)
  })
  const edges = before.filter(hash => hash !== commit.hash).map(hash => ({ from: before.indexOf(hash), to: after.indexOf(hash), parent: hash }))
  edges.push(...commit.parents.map(parent => ({ from: lane, to: after.indexOf(parent), parent })))
  width = Math.max(width, before.length, after.length)
  lanes = after
  return { lane, incoming, before, edges }
 })
 return { rows, width: width * 18 + 16 }
}

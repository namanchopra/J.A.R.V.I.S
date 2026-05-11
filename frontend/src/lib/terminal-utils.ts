/**
 * Strip the Unicode output marker (U+23BF) from each line of tool-output
 * content so we render clean text.
 */
export function cleanOutputContent(content: string): string {
  return content
    .split('\n')
    .map((line) => {
      const trimmed = line.trimStart()
      if (trimmed.startsWith('\u23BF')) {
        // Remove marker and the space that typically follows it
        return trimmed.slice(1).trimStart()
      }
      return line
    })
    .join('\n')
}

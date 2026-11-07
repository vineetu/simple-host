import { navigation } from './navigation-data.js'
// Resolve a stable map target without needing a runtime fetch.
export function resolve(targetID, slug, params = {}) {
  const t = navigation.targets.find(t => t.id === targetID)
  if (!t) throw new Error('Unknown navigation target: ' + targetID)
  const href = t.href_pattern.replace(/\{([^}]+)\}/g, (_, key) => {
    const value = key === 'slug' ? slug : params[key]
    if (value == null) throw new Error('Missing navigation parameter: ' + key)
    return encodeURIComponent(value)
  })
  return { screen: t.screen, section: t.section, tab: t.tab, anchor: t.anchor, href }
}


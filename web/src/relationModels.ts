import type { ContentModelSummary } from './api/types'

export function relationModelOptions(models: ContentModelSummary[], currentModelID: string) {
  return models.filter((item) => item.id !== currentModelID).map((item) => ({ value: item.id, label: item.display_name, searchText: `${item.display_name} ${item.key}`, key: item.key }))
}

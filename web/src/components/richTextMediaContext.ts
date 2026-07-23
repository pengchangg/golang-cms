import { createContext } from 'react'

import type { RichTextMediaEnvironment } from './richTextMedia'

export const RichTextMediaContext = createContext<RichTextMediaEnvironment | null>(null)

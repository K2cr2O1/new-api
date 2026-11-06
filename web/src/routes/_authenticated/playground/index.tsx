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
import { useState } from 'react'
import { createFileRoute, redirect } from '@tanstack/react-router'

import { Main } from '@/components/layout'
import { Playground } from '@/features/playground'
import { ImagePlayground } from '@/features/playground/components/image/image-playground'
import { isSidebarModuleEnabled } from '@/lib/nav-modules'

export const Route = createFileRoute('/_authenticated/playground/')({
  beforeLoad: () => {
    if (!isSidebarModuleEnabled('chat', 'playground')) {
      throw redirect({ to: '/dashboard' })
    }
  },
  component: PlaygroundPage,
})

type PlaygroundMode = 'chat' | 'image'

const MODE_TAB =
  'rounded-md px-3 py-1.5 text-sm font-medium transition-colors'
const MODE_TAB_ACTIVE = `${MODE_TAB} bg-primary text-primary-foreground`
const MODE_TAB_IDLE = `${MODE_TAB} text-muted-foreground hover:text-foreground`

function PlaygroundPage() {
  const [mode, setMode] = useState<PlaygroundMode>('chat')

  return (
    <Main className='p-0'>
      <div className='flex size-full min-h-0 flex-col'>
        <div className='flex items-center gap-1.5 border-b border-border bg-background px-4 py-2'>
          <button
            type='button'
            className={mode === 'chat' ? MODE_TAB_ACTIVE : MODE_TAB_IDLE}
            onClick={() => setMode('chat')}
          >
            对话
          </button>
          <button
            type='button'
            className={mode === 'image' ? MODE_TAB_ACTIVE : MODE_TAB_IDLE}
            onClick={() => setMode('image')}
          >
            生图
          </button>
          <span className='ml-2 text-xs text-muted-foreground'>
            {mode === 'image' ? '使用当前登录态调用，按分组倍率计费' : ''}
          </span>
        </div>
        <div className='min-h-0 flex-1 overflow-hidden'>
          {mode === 'chat' ? <Playground /> : <ImagePlayground />}
        </div>
      </div>
    </Main>
  )
}

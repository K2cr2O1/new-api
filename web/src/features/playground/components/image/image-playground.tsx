import { useEffect, useRef, useState } from 'react'

import { api } from '@/lib/api'
import { API_ENDPOINTS } from '../../constants'
import { getUserGroups, getUserModels } from '../../api'

interface ImageGenItem {
  url?: string
  b64_json?: string
  revised_prompt?: string
}

interface GalleryItem {
  id: number
  prompt: string
  model: string
  size: string
  src: string
}

const SIZES = ['1024x1024', '1792x1024', '1024x1792', '1280x720', '720x1280']

// 常见生图模型关键词：命中时排到列表前面
const IMAGE_HINTS = [
  'image', 'dall', 'flux', 'banana', 'seedream', 'sd', 'mj',
  'kolors', 'cogview', 'janus', 'irag', 'draw', 'pic',
]

function isLikelyImageModel(m: string) {
  return IMAGE_HINTS.some((h) => m.toLowerCase().includes(h))
}

export function ImagePlayground() {
  const [groups, setGroups] = useState<{ label: string; value: string }[]>([])
  const [group, setGroup] = useState('default')
  const [models, setModels] = useState<string[]>([])
  const [model, setModel] = useState('')
  const [prompt, setPrompt] = useState('')
  const [size, setSize] = useState('1024x1024')
  const [count, setCount] = useState(1)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [gallery, setGallery] = useState<GalleryItem[]>([])
  const idRef = useRef(1)

  // 分组
  useEffect(() => {
    getUserGroups()
      .then((gs) => {
        setGroups(gs)
        if (gs.length > 0) setGroup(gs[0].value)
      })
      .catch(() => setGroups([]))
  }, [])

  // 模型列表（跟随分组）
  useEffect(() => {
    if (!group) return
    getUserModels(group)
      .then((ms) => {
        const names = ms.map((m) => m.value)
        // 生图相关模型排前面，方便直接选
        names.sort((a, b) => Number(isLikelyImageModel(b)) - Number(isLikelyImageModel(a)))
        setModels(names)
        setModel((prev) => (prev && names.includes(prev) ? prev : names[0] ?? ''))
      })
      .catch(() => setModels([]))
  }, [group])

  const submit = async () => {
    const text = prompt.trim()
    if (!text || loading) return
    setLoading(true)
    setError('')
    try {
      const res = await api.post(
        API_ENDPOINTS.IMAGES_GENERATIONS,
        {
          model,
          group,
          prompt: text,
          n: count,
          size,
          response_format: 'b64_json',
        },
        { skipErrorHandler: true } as Record<string, unknown>
      )
      const payload = res.data
      const arr: ImageGenItem[] = Array.isArray(payload?.data) ? payload.data : []
      if (arr.length === 0) {
        setError(payload?.error?.message || '上游没有返回图片')
        return
      }
      const now = Date.now()
      const added: GalleryItem[] = arr.map((it, i) => ({
        id: now + i,
        prompt: text,
        model,
        size,
        src: it.b64_json ? `data:image/png;base64,${it.b64_json}` : (it.url ?? ''),
      })).filter((g) => g.src)
      if (added.length === 0) {
        setError('返回的图片数据为空')
        return
      }
      setGallery((prev) => [...added, ...prev])
    } catch (e: unknown) {
      const err = e as { response?: { data?: { error?: { message?: string } } }; message?: string }
      setError(err?.response?.data?.error?.message || err?.message || '生成失败')
    } finally {
      setLoading(false)
    }
  }

  const sel = 'h-9 rounded-md border border-border bg-background px-3 text-sm'

  return (
    <div className='size-full overflow-y-auto'>
      <div className='mx-auto max-w-4xl space-y-5 px-4 py-6'>
        {/* 控制区 */}
        <div className='rounded-xl border border-border bg-background p-4'>
          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
            <label className='space-y-1'>
              <span className='text-xs text-muted-foreground'>分组</span>
              <select className={`${sel} w-full`} value={group} onChange={(e) => setGroup(e.target.value)}>
                {groups.map((g) => (
                  <option key={g.value} value={g.value}>{g.label}</option>
                ))}
                {groups.length === 0 && <option value='default'>default</option>}
              </select>
            </label>
            <label className='space-y-1 sm:col-span-2 lg:col-span-1'>
              <span className='text-xs text-muted-foreground'>模型</span>
              <select className={`${sel} w-full`} value={model} onChange={(e) => setModel(e.target.value)}>
                {models.map((m) => (
                  <option key={m} value={m}>{m}</option>
                ))}
              </select>
            </label>
            <label className='space-y-1'>
              <span className='text-xs text-muted-foreground'>尺寸</span>
              <select className={`${sel} w-full`} value={size} onChange={(e) => setSize(e.target.value)}>
                {SIZES.map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </select>
            </label>
            <label className='space-y-1'>
              <span className='text-xs text-muted-foreground'>数量</span>
              <select className={`${sel} w-full`} value={count} onChange={(e) => setCount(Number(e.target.value))}>
                {[1, 2, 3, 4].map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
              </select>
            </label>
          </div>

          <textarea
            className='mt-3 min-h-24 w-full resize-y rounded-md border border-border bg-background p-3 text-sm outline-none transition-colors focus:border-primary'
            placeholder='描述你想生成的画面…'
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) submit()
            }}
          />

          <div className='mt-3 flex items-center gap-3'>
            <button
              type='button'
              onClick={submit}
              disabled={loading || !prompt.trim() || !model}
              className='inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground transition-opacity disabled:opacity-50'
            >
              {loading ? '生成中…' : '生成图片'}
            </button>
            <span className='text-xs text-muted-foreground'>Ctrl + Enter 快速提交 · 按分组倍率计费</span>
          </div>

          {error && (
            <p className='mt-3 rounded-md border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive'>
              {error}
            </p>
          )}
        </div>

        {/* 画廊 */}
        {gallery.length === 0 ? (
          <p className='py-10 text-center text-sm text-muted-foreground'>
            还没有生成记录，输入提示词开始吧
          </p>
        ) : (
          <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3'>
            {gallery.map((g) => (
              <figure key={g.id} className='overflow-hidden rounded-xl border border-border bg-background'>
                <img src={g.src} alt={g.prompt} className='w-full bg-muted' />
                <figcaption className='space-y-2 p-3'>
                  <p className='line-clamp-2 text-xs text-muted-foreground' title={g.prompt}>{g.prompt}</p>
                  <p className='font-mono text-[11px] text-muted-foreground/70'>
                    {g.model} · {g.size}
                  </p>
                  <div className='flex gap-2'>
                    <a
                      href={g.src}
                      download={`wb-${g.id}.png`}
                      target='_blank'
                      rel='noreferrer'
                      className='rounded-md border border-border px-2 py-1 text-xs transition-colors hover:border-primary'
                    >
                      下载
                    </a>
                    <button
                      type='button'
                      className='rounded-md border border-border px-2 py-1 text-xs transition-colors hover:border-primary'
                      onClick={() => setPrompt(g.prompt)}
                    >
                      重用提示词
                    </button>
                  </div>
                </figcaption>
              </figure>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

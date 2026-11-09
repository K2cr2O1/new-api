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
import { useEffect, useId, useRef, useState } from 'react'

import '@/features/auth/lib/aliyun-captcha-types'
import type { AliyunCaptchaInitOptions } from '@/features/auth/lib/aliyun-captcha-types'

interface AliyunCaptchaProps {
  sceneId: string
  prefix: string
  region: string
  onVerify: (captchaVerifyParam: string) => void
  onError?: () => void
  className?: string
  /** 父组件持有的验证票据；票据被清空（已消费/提交失败）后控件回到可再次验证 */
  verified?: boolean
}

/**
 * 从业务后端获取加密模式所需的 EncryptedSceneId。
 * 加密模式未开启或接口异常时返回空字符串，前端降级为明文 SceneId。
 */
async function getEncryptedSceneId(): Promise<string> {
  const res = await fetch('/api/captcha/encrypted-scene-id', {
    cache: 'no-store',
  })
  if (!res.ok) throw new Error('http ' + res.status)
  const json = await res.json()
  if (json?.success && json?.data?.EncryptedSceneId) {
    return json.data.EncryptedSceneId as string
  }
  throw new Error('no encrypted scene id in response')
}

/**
 * 阿里云验证码 2.0 弹出式组件。
 *
 * 交互：渲染一个「点击完成人机验证」按钮，用户点击后弹出阿里云验证码；
 * 通过后 onVerify 拿到 captchaVerifyParam，父组件保存该参数并在业务请求里
 * 通过 captchaVerifyParam 字段（或 X-Captcha-Param 头）提交。
 *
 * 约束（官方文档）：
 * - SDK 必须动态加载，不能本地部署
 * - region + prefix 通过 window.AliyunCaptchaConfig 传入（region 仅 cn/sgp）
 * - initAliyunCaptcha 不支持重复调用，用 ref 守卫
 * - 加密模式：初始化前从后端获取 EncryptedSceneId 传入
 */
export function AliyunCaptcha({
  sceneId,
  prefix,
  region,
  onVerify,
  onError,
  verified,
  className,
}: AliyunCaptchaProps) {
  const [state, setState] = useState<'idle' | 'verifying' | 'done' | 'error'>(
    'idle'
  )
  const elId = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  const btnId = `${elId}-btn`
  const ref = useRef<HTMLDivElement | null>(null)
  const initializedRef = useRef(false)
  const onVerifyRef = useRef(onVerify)
  const onErrorRef = useRef(onError)
  onVerifyRef.current = onVerify
  onErrorRef.current = onError

  // 票据被父组件清空（发信已消费 / 提交失败）后必须回到 idle，
  // 否则按钮一直是 disabled 的「人机验证已完成」，用户只能刷新页面。
  useEffect(() => {
    if (!verified) {
      setState((prev) => (prev === 'done' ? 'idle' : prev))
    }
  }, [verified])

  // 兜底：SDK 未回调（弹窗被拦截、脚本异常）时不要永远停在「验证中…」
  useEffect(() => {
    if (state !== 'verifying') return
    const timer = window.setTimeout(() => {
      setState((prev) => (prev === 'verifying' ? 'idle' : prev))
      onErrorRef.current?.()
    }, 45000)
    return () => window.clearTimeout(timer)
  }, [state])

  useEffect(() => {
    // 官方要求：AliyunCaptchaConfig 必须在 SDK 脚本加载之前写入
    window.AliyunCaptchaConfig = { region, prefix }
    if (initializedRef.current) return

    const init = async () => {
      if (!window.initAliyunCaptcha) {
        setState('error')
        onErrorRef.current?.()
        return
      }
      initializedRef.current = true
      try {
        // 加密模式：优先获取 EncryptedSceneId；失败则降级为明文 SceneId（兼容未开启）
        let encryptedSceneId = ''
        try {
          encryptedSceneId = await getEncryptedSceneId()
        } catch {
          encryptedSceneId = ''
        }

        const opts: AliyunCaptchaInitOptions = {
          SceneId: sceneId,
          mode: 'popup',
          element: `#${elId}`,
          button: `#${btnId}`,
          success: (param: string) => {
            setState('done')
            onVerifyRef.current(param)
          },
          fail: () => {
            setState('idle')
            onErrorRef.current?.()
          },
          language: 'cn',
        }
        if (encryptedSceneId) {
          opts.EncryptedSceneId = encryptedSceneId
        }
        window.initAliyunCaptcha(opts)
      } catch {
        setState('error')
        onErrorRef.current?.()
      }
    }

    if (window.initAliyunCaptcha) {
      init()
      return
    }

    const scriptId = 'aliyun-captcha'
    const existing = document.getElementById(
      scriptId
    ) as HTMLScriptElement | null
    if (existing) {
      existing.addEventListener('load', init, { once: true })
      return
    }
    const s = document.createElement('script')
    s.id = scriptId
    s.src =
      'https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js'
    s.async = true
    s.onload = () => init()
    s.onerror = () => {
      setState('error')
      onErrorRef.current?.()
    }
    document.head.appendChild(s)
  }, [sceneId, prefix, region, elId, btnId])

  const label =
    state === 'done'
      ? '人机验证已完成'
      : state === 'verifying'
        ? '验证中…'
        : state === 'error'
          ? '人机验证加载失败，请刷新重试'
          : '点击完成人机验证'

  return (
    <div className={className}>
      <button
        id={btnId}
        type="button"
        onClick={() => setState('verifying')}
        disabled={state === 'done'}
        className={`text-sm underline-offset-4 hover:underline ${state === 'error' ? 'text-red-500' : 'text-muted-foreground'}`}
      >
        {label}
      </button>
      {/* 阿里云验证码的渲染锚点（element 参数指向它，popup 弹窗渲染于此，不能 hidden） */}
      <div id={elId} ref={ref} />
    </div>
  )
}

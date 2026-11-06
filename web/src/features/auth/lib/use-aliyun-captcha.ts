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
import { useStatus } from '@/hooks/use-status'

/**
 * 阿里云验证码 2.0（Captcha）配置读取 hook。
 *
 * 与 Turnstile 的 useTurnstile 对称：从 /api/status 读取开关和
 * sceneId/prefix/region，供 AliyunCaptcha 组件使用。
 *
 * 注意：本 hook 只负责读配置，验证码的初始化与交互在
 * <AliyunCaptcha> 组件里完成（需要动态加载 SDK + initAliyunCaptcha）。
 */
export function useAliyunCaptcha() {
  const { status } = useStatus()

  const enabled = !!status?.aliyun_captcha_enabled
  const sceneId = (status?.aliyun_captcha_scene_id as string) || ''
  const prefix = (status?.aliyun_captcha_prefix as string) || ''
  const rawRegion = (status?.aliyun_captcha_region as string) || ''
  // 服务端 endpoint region（cn-shanghai）≠ 前端 SDK region（仅支持 cn/sgp）
  const region = rawRegion === 'sgp' ? 'sgp' : 'cn'

  // 三个必填项齐全才认为可用，缺一项就不渲染验证码（避免用户被卡死）
  const ready = enabled && !!sceneId && !!prefix

  return { ready, sceneId, prefix, region }
}

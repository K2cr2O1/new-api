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

// 阿里云验证码 2.0（Captcha）前端类型声明。
// 官方要求：在加载 https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js
// 之前设置 window.AliyunCaptchaConfig = { region, prefix }，SDK 加载后挂载
// window.initAliyunCaptcha。这里把两个全局对象的形状声明出来，供 TS 使用。

export interface AliyunCaptchaConfig {
  // 必填，验证码实例所属区域：cn（中国大陆）/ sgp（新加坡）
  region: string
  // 必填，身份标识，控制台「实例基本信息」卡片里获取
  prefix: string
}

export interface AliyunCaptchaInitOptions {
  // 场景 ID，控制台「场景管理」里获取
  SceneId: string
  // 加密模式：加密后的 SceneId（可选，开启加密模式时由后端生成）
  EncryptedSceneId?: string
  // 验证码模式：popup 弹出式 / embed 嵌入式
  mode: 'popup' | 'embed'
  // 预留渲染元素的选择器
  element: string
  // 触发验证码的元素选择器
  button?: string
  // 验证通过回调，入参 captchaVerifyParam，需要随业务请求提交给后端校验
  success: (captchaVerifyParam: string) => void
  // 验证失败回调
  fail?: (result: unknown) => void
  // 绑定验证码实例回调
  getInstance?: (instance: unknown) => void
  // 滑条样式（可选）
  slideStyle?: { width: number; height: number }
  // 语言：cn / tw / en
  language?: 'cn' | 'tw' | 'en'
  // 完成验证后是否立即发验证请求（嵌入式用）
  immediate?: boolean
}

declare global {
  interface Window {
    AliyunCaptchaConfig?: AliyunCaptchaConfig
    initAliyunCaptcha?: (options: AliyunCaptchaInitOptions) => void
  }
}

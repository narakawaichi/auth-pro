import { RouterLink } from 'vue-router'
import type { Component } from 'vue'

/** 渲染广告卡片外壳所需的标签与属性；无投放链接时退化为普通容器 */
export interface PromotionLink {
  is: Component | string
  props: Record<string, unknown>
}

const isExternalUrl = (url: string) => /^https?:\/\//i.test(url)

/** 广告投放项的通用行为：链接解析与统一的珍珠灰图片底色 */
export function usePromotion() {
  /**
   * 站内地址交给路由链接，避免整页刷新；两种情况都会渲染成真实的 a 标签，
   * 用户可以右键新开、中键打开、悬停查看目标。
   */
  const resolvePromotionLink = (url: string): PromotionLink => {
    if (!url) return { is: 'div', props: {} }
    if (isExternalUrl(url)) {
      return {
        is: 'a',
        props: { href: url, target: '_blank', rel: 'noopener noreferrer' }
      }
    }
    return { is: RouterLink, props: { to: url } }
  }

  const coverStyle = () => ({
    background: 'linear-gradient(135deg, #f7f6f3, #e7e6e2)'
  })

  return { resolvePromotionLink, coverStyle }
}

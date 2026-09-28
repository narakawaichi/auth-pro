<!-- 广告位展示板：按数据页的 layout 分发布局，渲染层不决定投放数量与位置 -->
<template>
  <div class="art-card promotion-board" :style="{ height: height }">
    <div class="board-header">
      <div>
        <h3>{{ title }}</h3>
        <p v-if="subtitle">{{ subtitle }}</p>
      </div>
      <slot name="extra"></slot>
    </div>

    <div v-if="pages.length > 0" class="promotion-carousel">
      <ElCarousel
        :indicator-position="pages.length > 1 ? 'outside' : 'none'"
        arrow="hover"
        :interval="interval"
        :autoplay="pages.length > 1"
      >
        <ElCarouselItem v-for="page in renderPages" :key="page.id">
          <div class="promotion-page" :class="`is-${page.layout}`">
            <component
              :is="cell.link.is"
              v-for="cell in page.cells"
              :key="cell.item.id"
              v-bind="cell.link.props"
              class="promotion-item"
              :class="{ 'is-clickable': !!cell.item.linkUrl }"
            >
              <div class="promotion-cover" :style="coverStyle()">
                <img v-if="cell.item.imageUrl" :src="cell.item.imageUrl" :alt="cell.item.title" />
                <span v-else>{{ cell.item.title.charAt(0) }}</span>
              </div>
              <div class="promotion-body">
                <div class="promotion-heading">
                  <p class="promotion-title">{{ cell.item.title }}</p>
                  <span class="promotion-tag">{{ cell.item.tag }}</span>
                </div>
                <small class="promotion-summary">{{ cell.item.summary }}</small>
              </div>
            </component>
          </div>
        </ElCarouselItem>
      </ElCarousel>
    </div>
    <ElEmpty v-else :description="emptyText" />
  </div>
</template>

<script setup lang="ts">
  import { computed } from 'vue'
  import type { PromotionPage } from '@/api/promotion'
  import { usePromotion } from '@/hooks'
  import type { PromotionLink } from '@/hooks'

  defineOptions({ name: 'ArtPromotionBoard' })

  const props = withDefaults(
    defineProps<{
      pages: PromotionPage[]
      title?: string
      subtitle?: string
      /** 板高，必须是确定值；轮播项为绝对定位，靠 min-height 撑不起来 */
      height?: string
      emptyText?: string
      interval?: number
    }>(),
    {
      title: '推荐服务',
      subtitle: '',
      height: '100%',
      emptyText: '暂无推荐内容',
      interval: 6000
    }
  )

  const { resolvePromotionLink, coverStyle } = usePromotion()

  const renderPages = computed(() =>
    props.pages.map((page) => ({
      id: page.id,
      layout: page.layout,
      cells: page.items.map((item) => ({
        item,
        link: resolvePromotionLink(item.linkUrl) as PromotionLink
      }))
    }))
  )
</script>

<style lang="scss" scoped>
  .promotion-board {
    display: flex;
    flex-direction: column;
    width: 100%;
    padding: 20px;
    margin-bottom: 16px;
  }

  .board-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 18px;

    h3 {
      margin: 0;
      font-size: 17px;
      font-weight: 700;
    }

    p {
      margin-top: 6px;
      font-size: 13px;
      color: var(--art-gray-500);
    }
  }

  .promotion-carousel {
    flex: 1;
    min-height: 0;

    :deep(.el-carousel) {
      display: flex;
      flex-direction: column;
      height: 100%;
    }

    :deep(.el-carousel__container) {
      flex: 1;
      min-height: 0;
    }

    :deep(.el-carousel__indicators--outside) {
      margin-top: 6px;
    }

    :deep(.el-carousel__indicators--outside button) {
      background-color: var(--art-gray-400);
    }

    :deep(.el-carousel__indicator.is-active button) {
      background-color: var(--art-primary);
    }
  }

  .promotion-page {
    height: 100%;

    &.is-grid {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      grid-auto-rows: auto;
      gap: 10px;

      // 投放图固定 16:9：行高随内容，图片不被拉伸变形
      .promotion-cover {
        flex: none;
        aspect-ratio: 16 / 9;
      }
    }

    &.is-banner {
      display: flex;

      .promotion-item {
        flex: 1;
      }

      .promotion-cover {
        span {
          font-size: 46px;
        }
      }

      .promotion-body {
        gap: 6px;
        padding: 14px 16px 16px;
      }

      .promotion-title {
        font-size: 17px;
      }

      .promotion-tag {
        padding: 2px 8px;
        font-size: 12px;
      }

      .promotion-summary {
        font-size: 13px;
        -webkit-line-clamp: 3;
      }
    }
  }

  .promotion-item {
    display: flex;
    flex-direction: column;
    min-width: 0;
    overflow: hidden;
    color: inherit;
    text-decoration: none;
    background: var(--art-gray-100);
    border: 1px solid transparent;
    border-radius: 12px;
    transition:
      border-color 0.2s ease,
      box-shadow 0.2s ease,
      transform 0.2s ease;

    &.is-clickable {
      cursor: pointer;

      &:hover {
        border-color: transparent;
        box-shadow: 0 6px 16px rgb(0 0 0 / 6%);
        transform: translateY(-2px);
      }
    }
  }

  .promotion-cover {
    display: flex;
    flex: 1;
    align-items: center;
    justify-content: center;
    min-height: 32px;
    overflow: hidden;
    background: var(--art-primary);

    img {
      width: 100%;
      height: 100%;
      object-fit: cover;
    }

    span {
      font-size: 20px;
      font-weight: 700;
      color: var(--art-gray-700);
    }
  }

  .promotion-body {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 6px 8px 8px;
  }

  .promotion-heading {
    display: flex;
    gap: 6px;
    align-items: center;
    min-width: 0;
  }

  .promotion-tag {
    flex-shrink: 0;
    padding: 1px 5px;
    font-size: 10px;
    line-height: 1.6;
    color: var(--art-primary);
    background: rgba(var(--art-primary-rgb), 0.1);
    border-radius: 5px;
  }

  .promotion-title {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    font-size: 13px;
    font-weight: 600;
    color: var(--art-gray-900);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .promotion-summary {
    display: -webkit-box;
    overflow: hidden;
    font-size: 11px;
    line-height: 1.5;
    color: var(--art-gray-500);
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
  }

  @media (width <= 768px) {
    .promotion-page.is-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>

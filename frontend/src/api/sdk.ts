import request from '@/utils/http'

/** 下载指定语言的 SDK 示例包（zip），lang 目前支持 php */
export function fetchDownloadSdk(lang: string) {
  return request.get<Blob>({
    url: `/api/sdk/${lang}/download`,
    responseType: 'blob'
  })
}
